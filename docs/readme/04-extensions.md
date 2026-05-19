# 04 — Extensions

## Codec & bridge

YAML, JSON, and TOML are registered at `init` by `pkg/decoder`. You do
not need to call `RegisterCodec` for these formats — they are immediately
available to the discovery layer and to providers that take a `Codec`.

The decode bridge controls how the merged `map[string]any` becomes
`*T`:

```go
fastconf.WithCodecBridge(fastconf.BridgeJSON) // default — uses encoding/json
fastconf.WithCodecBridge(fastconf.BridgeYAML) // uses gopkg.in/yaml.v3
```

Use `BridgeYAML` when your struct fields only carry `yaml` tags. Use
`BridgeJSON` (the default) for structs with `json` tags or anything that
also goes through `encoding/json` elsewhere.

To register a custom codec (e.g. HCL, JSON5) at runtime:

```go
fastconf.RegisterCodec("hcl", hclCodec{})
fastconf.RegisterCodecExt("hcl", "hcl") // .hcl files now route to that codec
```

---

## Transformers & migration

### Transformer interface

```go
type Transformer interface {
    Transform(root map[string]any) error
    Name() string
}
```

Transformers run after merge and before decode; they receive the merged
`map[string]any` and may safely mutate the tree.

### Built-in transformers (`pkg/transform`)

```go
import "github.com/fastabc/fastconf/pkg/transform"

fastconf.WithTransformers(
    transform.Defaults(map[string]any{                 // recursive merge — does not overwrite
        "server": map[string]any{"timeout": "30s"},
    }),
    transform.SetIfAbsent("server.timeout", "30s"),    // single-path default
    transform.EnvSubst(),                              // ${VAR} / ${VAR:-default} / ${VAR:?required message}
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{               // old path → new path
        "db.url":      "database.dsn",
        "server.port": "server.addr",
    }),
)
```

### Struct tags

```go
type AppConfig struct {
    Server struct {
        Addr    string        `json:"addr"    fastconf:"default=:8080"`
        Timeout time.Duration `json:"timeout" fastconf:"default=30s"`
    } `json:"server"`
    Database struct {
        DSN string `json:"dsn" fastconf:"secret"` // redacted in logs/snapshots
    } `json:"database"`
}

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithStructDefaults[AppConfig](),
    fastconf.WithSecretRedactor(fastconf.DefaultSecretRedactor),
)
```

`fastconf:"default=…"` runs after decode and before validate, only
populating zero values. Field-meta tags (`range=`, `enum=`, `required`)
are checked in the same stage.

### Migration

```go
import "github.com/fastabc/fastconf/pkg/migration"

chain := migration.NewChain(
    migration.Step{From: "1", To: "2", Apply: migrateV1toV2},
    migration.Step{From: "2", To: "3", Apply: migrateV2toV3},
)
fastconf.WithMigrations(chain.Migrate)
```

Or inline:

```go
fastconf.WithMigrations(func(root map[string]any) error {
    if v, ok := root["db_url"]; ok {
        db, _ := root["database"].(map[string]any)
        if db == nil { db = map[string]any{}; root["database"] = db }
        if _, has := db["dsn"]; !has { db["dsn"] = v }
        delete(root, "db_url")
    }
    return nil
})
```

---

## Watch, Subscribe, and Plan

### Filesystem watch

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
    // Defaults to ProfileK8s (quiet=30ms / maxLag=250ms / swapHint=5ms).
    // Switch presets, or tweak one knob:
    fastconf.WithCoalesce(fastconf.CoalesceOptions{Quiet: 50 * time.Millisecond}),
)
// Kubernetes ConfigMap ..data symlink atomic swaps are handled correctly
// by watching the parent directory; swap-commit detection tightens the
// burst window to swapHint (5ms), and per-dir keying prevents multiple
// ConfigMaps from blocking each other.
```

### Field-level Subscribe

```go
cancel := fastconf.Subscribe(mgr,
    func(app *AppConfig) *DatabaseConfig { return &app.Database },
    func(old, neu *DatabaseConfig) {
        if old != nil && *old == *neu { return } // caller-side diff
        reconnect(neu.DSN)
    },
)
defer cancel()
```

Subscribe callbacks fire synchronously on the reload goroutine (a
`recover()` shields the loop from a panicking subscriber). For long
work, spawn a goroutine yourself.

### Manual reload with one-shot override

```go
err := mgr.Reload(ctx,
    fastconf.WithReloadReason("admin-cli"),
    fastconf.WithSourceOverride(map[string]any{
        "server": map[string]any{"addr": ":9999"},
    }),
)
```

### Pause / Resume

```go
mgr.Watcher().Pause()
applyBatchUpdate()
mgr.Watcher().Resume()
```

### Plan (dry-run)

```go
result, err := mgr.Plan().WithHostname("ci-runner-7").Run(ctx)
if err != nil {
    log.Fatal("plan failed:", err)
}
for _, r := range result.Validators {
    if r.Err != nil {
        log.Printf("validator %s failed: %v", r.Name, r.Err)
    }
}
for _, v := range result.Policies {
    log.Printf("[%s] %s @ %s — %s", v.Severity, v.Rule, v.Path, v.Message)
}
```

`Plan` never updates the atomic pointer; `SeverityError` policy
violations are downgraded to warnings in dry-run mode so CI can collect
every problem in a single pass.

---

## Provenance, history & rollback

### Provenance

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvenance(fastconf.ProvenanceFull),
)

origins := mgr.Snapshot().Explain("server.addr")
for _, o := range origins {
    fmt.Printf("layer=%s priority=%d value=%v\n", o.Source.Name, o.Source.Priority, o.Value)
}

// Strict lookup — distinguishes "provenance not enabled" from "path not found".
origins, err := mgr.Snapshot().LookupStrict("database.dsn")
```

| Level | Cost | What you can trace |
|---|---|---|
| `ProvenanceOff` | zero | nothing |
| `ProvenanceTopLevel` | O(top-level keys) | which layer set each top-level field |
| `ProvenanceFull` | O(leaves) | full override chain per leaf, with each layer's raw value |

### History & rollback

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithHistory(10),
)

history := mgr.Replay().List()        // []*State[T], oldest → newest
target  := history[len(history)-2]    // previous version
_ = mgr.Replay().Rollback(target)
```

`Rollback` re-publishes a historic `*State[T]` to the atomic pointer; it
does not re-run the pipeline and does not bump `Generation`, but it does
fire Subscribe callbacks (filter on the caller side if you care).

### Errors stream

```go
go func() {
    for re := range mgr.Errors() {
        slog.Error("reload failed", "reason", re.Reason, "err", re.Err, "when", re.When)
    }
}()
```

Buffer 16, drop-on-full. The "keep old state on failure" contract is
unchanged regardless of whether anyone reads this channel.

---

## Observability

### AuditSink

```go
type AuditSink interface {
    Audit(ctx context.Context, cause ReloadCause) error
}

sink := fastconf.NewJSONAuditSink(os.Stderr) // built-in JSON-lines sink
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithAuditSink(sink),
    fastconf.WithAuditSink(remoteSink), // multiple sinks fan out
)
// Output: {"reason":"watcher","at":"2026-05-14T08:00:00Z","revisions":{"vault":"42"}}
```

### MetricsSink

```go
type MetricsSink interface {
    ReloadStarted()
    ReloadFinished(ok bool, dur time.Duration)
    // Optional extensions: ProviderMetricsSink / StageMetricsSink / RenderMetricsSink
}
```

A Prometheus implementation lives in a separate sub-module:

```go
import prommetrics "github.com/fastabc/fastconf/observability/metrics/prometheus"

mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithMetrics(prommetrics.New()))
```

### Tracer (OpenTelemetry)

Default is no-op. OTel SDK integration lives in a sub-module:

```go
import fastconfotel "github.com/fastabc/fastconf/observability/otel"

tracer := fastconfotel.NewTracer(otel.GetTracerProvider())
mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithTracer(tracer))
```

Build with `-tags fastconf_otel` to enable enriched span attributes.

### DiffReporter

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDiffReporter(fastconf.DiffReporterFunc(
        func(ctx context.Context, ev fastconf.DiffEvent) error {
            return slack.Post(ctx, ev.Diff) // runs async; never blocks reload
        },
    )),
    fastconf.WithDiffReporterQueueCap(128), // default 64
)
```

Each reporter has its own bounded-queue worker:

- Enqueue is non-blocking; reload never waits on a slow reporter.
- Queue full → event dropped, `MetricsSink.EventDropped("diff-reporter")` fires.
- `Manager.Close()` drains workers via `bgWG.Wait()` — no leaks.

### Policy

```go
import "github.com/fastabc/fastconf/policy"

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithPolicy(policy.Func[AppConfig]{
        N: "deny-debug-in-prod",
        Fn: func(_ context.Context, in policy.Input[AppConfig]) ([]policy.Violation, error) {
            if in.Config.Env == "prod" && in.Config.Debug {
                return []policy.Violation{{
                    Rule:     "deny-debug-in-prod",
                    Path:     "debug",
                    Message:  "debug mode must be false in prod",
                    Severity: policy.SeverityError, // aborts reload
                }}, nil
            }
            return nil, nil
        },
    }),
)
```

CUE and OPA implementations live in `cue/policy` and `policy/opa`.

| Severity | Plan behaviour | Reload behaviour |
|---|---|---|
| `SeverityWarning` | logged, continues | logged, continues |
| `SeverityError` | downgraded to warning (dry-run collects everything) | aborts reload; old state preserved |

---

## Multi-tenant & presets

### `TenantManager[T]`

```go
tm := fastconf.NewTenantManager[AppConfig]()

mgrA, _ := tm.Add(ctx, "tenant-a",
    fastconf.WithDir("/etc/config/tenant-a"),
    fastconf.WithProfile(fastconf.ProfileOptions{EnvVar: "TENANT_A_PROFILE"}),
)
mgrB, _ := tm.Add(ctx, "tenant-b",
    fastconf.WithDir("/etc/config/tenant-b"),
    fastconf.WithProvider(tenantBVaultProvider),
)

app, err := tm.Get("tenant-a") // *AppConfig, error (fastconf.ErrUnknownTenant)
_ = tm.Remove("tenant-a")      // calls the underlying Manager.Close()
tm.Close()
```

Each tenant is fully isolated; AuditSink receives `Cause.Tenant = id`.

### Presets

```go
// Standard Kubernetes ConfigMap deployment.
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.PresetK8s(fastconf.K8sOpts{
        Dir: "/etc/config", ProfileEnv: "APP_PROFILE", Default: "default", Watch: true,
    }),
    fastconf.WithStrict(false), // override the preset's strict=true
)

// fastconfd sidecar.
fastconf.PresetSidecar(fastconf.SidecarOpts{
    Dir: "/etc/fastconfd", HistoryN: 16, Watch: true, Strict: false,
})

// Test fixture: an in-process fs.FS for a known profile.
fastconf.PresetTesting(fastconf.TestingOpts{
    FS:      memFS,        // fs.FS
    Profile: "testing",
})

// Region / zone / host axis overlays.
fastconf.PresetHierarchical(fastconf.HierarchicalOpts{ /* ... */ })
```

---

