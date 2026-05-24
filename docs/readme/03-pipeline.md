# 03 — Pipeline

## Reload pipeline

### Triggers

```
                          ┌── fsnotify events → debounce 500ms ──┐
                          │                                       │
Reload(ctx, opts...) ─────┤    reloadCh chan reloadRequest       ├──► reloadLoop
                          │                                       │    (single writer)
provider.Watch events ────┘── backoff + drop-on-full ──────────┘
```

### Stage sequence

```
reloadCh.recv(req)
  │
  ├─ stageMerge:      discovery.Scan(dir) → decode files → merger.Merge(layers)
  │                   apply _meta.yaml (appendSlices / profileEnv / match)
  │                   apply _patch.json (RFC 6902)
  │
  ├─ stageAssemble:   for each provider: Load(ctx) → merge by Priority
  │
  ├─ stageMigrate:    opts.migrationRun(merged)       [optional]
  ├─ stageTransform:  for each transformer: t.Transform(merged)
  ├─ stageDecode:     json.Marshal(merged) → json.Unmarshal(→ *T)
  │                   apply fastconf:"default=…" struct tags
  ├─ stageFieldMeta:  range / enum / required checks
  ├─ stageValidate:   for each validator: v(*T)
  ├─ stagePolicy:     for each policy:    p.Evaluate(ctx, *T, reason, tenant)
  │
  └─ commit:
       canonicalHashBytes(mergedJSON) → SHA-256 dedup
       atomic.Pointer.Store(newState)
       history.push(newState)
       for each AuditSink: Audit(ctx, cause)
       fireWatches(oldPartHashes, newPartHashes)
```

### Failure-safe semantics

When any stage returns a non-nil error:

- `atomic.Pointer` is **not** updated; `Get()` keeps returning the old value.
- `Generation` is **not** incremented.
- The error is returned synchronously from `Reload(ctx)`; the same event
  is also broadcast asynchronously on `Errors()`.
- **No AuditSink fires** — audit only triggers after a successful commit.
- `MetricsSink.ReloadFinished(ok=false, dur)` is called.

### Context propagation

The `ctx` passed to `Reload(ctx)` does more than control enqueue/wait — it
threads into the running pipeline:

- `assemble` short-circuits on `ctx.Err()`.
- Each `provider.Load(ctx)` shares the same ctx; slow providers
  bail out immediately on cancel.
- Cancellation errors propagate as `context.Canceled` /
  `context.DeadlineExceeded` (not wrapped in `ErrDecode`), so callers
  can `errors.Is(err, context.Canceled)` precisely.

Filesystem and provider watcher loops have no caller ctx; the framework
uses `context.Background()` for those paths to preserve event-driven
reload semantics.

---

## Profiles & overlays

### Layout

```
conf.d/
  base/                   # shared defaults for every profile
    00-defaults.yaml
    10-feature-flags.yaml
  overlays/
    dev/                  # applied when profile == "dev"
      50-dev.yaml
    prod/
      50-prod.yaml
      _meta.yaml          # profile match expression
      _patch.json         # RFC 6902 patch
    staging/
      50-staging.yaml
      _meta.yaml
```

### `_meta.yaml` fields

```yaml
schemaVersion: "1"
profileEnv: "APP_PROFILE"     # env var to read profile (overridden by WithProfile{EnvVar})
defaultProfile: "dev"         # fallback profile
appendSlices: true            # slices append instead of overwrite
match: "prod | staging"       # boolean profile expression (&, |, !, () supported)
```

`match` is compiled by `pkg/profile`:

| Syntax | Meaning |
|---|---|
| `prod` | profile set contains `"prod"` |
| `prod \| staging` | contains prod or staging |
| `prod & !debug` | prod and not debug |
| `(eu-west \| eu-east) & !debug` | composite |

### RFC 6902 JSON Patch

Drop a `_patch.json` into any overlay directory; FastConf applies it
after the layer's files merge:

```json
[
  { "op": "replace", "path": "/server/addr",      "value": ":8443" },
  { "op": "add",     "path": "/feature/darkMode", "value": true },
  { "op": "remove",  "path": "/legacy/key" }
]
```

### Multi-profile mode

```go
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProfile(fastconf.ProfileOptions{
        Multi: []string{"prod", "eu-west", "canary"},
    }),
)
```

`ProfileOptions.Single` and `.Multi` are mutually exclusive — set one or the
other, not both. In multi-profile mode each overlay's `_meta.yaml.match`
decides whether it applies.

---

## Provider system

### Built-in byte-blob sources (`pkg/source`)

Pair each Source with a Parser via `WithSource(src, parser)`. Passing
`nil` Parser auto-binds via the content-type hint (file extension,
HTTP `Content-Type` header, or `ContentType` ctor argument).

| Source | Constructor | Notes |
|---|---|---|
| File  | `source.NewFile(path)` | Reads the file at load time; content-type from extension |
| HTTP  | `source.NewHTTP(url)` | Conditional GET with ETag short-circuit; content-type from `Content-Type` header |
| Bytes | `source.NewBytes(name, contentType, data)` | In-memory layer (most common in tests) |

### Built-in parsers (`pkg/parser`)

| Parser | Content-types claimed |
|---|---|
| `parser.YAML()` | `yaml` / `.yaml` / `.yml` / `application/yaml` / `application/x-yaml` / `text/yaml` |
| `parser.JSON()` | `json` / `.json` / `application/json` / `text/json` |
| `parser.TOML()` | `toml` / `.toml` / `application/toml` / `text/toml` |

Third-party parsers register their content-types via `parser.Register`.

### Built-in structured providers (`pkg/provider`)

These contribute `map[string]any` directly — no Parser needed.

| Provider | Constructor | Notes |
|---|---|---|
| Env         | `provider.NewEnv("APP_")` | Default `DotReplacer`: `APP_FOO_BAR` → `foo.bar` (single `_`, Viper / Spring style). Values stay as strings; typed decoder converts. Chain `.WithReplacer(DoubleUnderscoreReplacer)`, `.At("path")`, `.WithCoerce(true)` as needed. |
| CLI         | `provider.NewCLI(map[string]any)` | Pass only explicitly changed CLI flags; omit parser defaults so files/env remain authoritative unless the user typed an override |
| DotEnv      | `provider.NewDotEnv("APP_", paths...)` | Explicit `.env` fallback paths; actual process env values win even when set to `""`. Same replacer / `At` / `WithCoerce` knobs as `NewEnv` |
| Labels      | `provider.NewLabels(labels, provider.LabelOptions{})` | Low-level flat-label primitive. Default priority `PriorityStatic`; pass a higher band explicitly when the source should override |
| DottedLabels| `provider.NewDottedLabels(labels, provider.DottedLabelOptions{})` | Explicit dotted-config labels when the key path itself is the whole DSL |
| RoutingLabels| `provider.NewRoutingLabels(labels, provider.RoutingLabelOptions{})` | Routing DSL labels with typed scalars, comma lists, `[N]` indexes, and an optional enable gate. For Traefik-style inputs, opt into the matching `Prefix`, `EnableGate`, and `LowercaseKeys` settings explicitly |
| LabelMap    | `provider.NewLabelMap(labels, provider.LabelOptions{})` | `map[string]string` variant of the low-level primitive |
| K8s Downward| `k8s.NewDefault()` (`providers/k8s`) | Reads `/etc/podinfo/{labels,annotations}` as raw metadata under `k8s.metadata.*`; mounted files automatically join the shared fs watcher when `WithWatch(WatchOptions{Enabled: true})` is enabled. Use `NewExpandedDefault()` or `MetadataExpanded` only when you intentionally want config-style expansion |

### First-party KV providers in the root module (`providers/{vault,consul,http}`)

```go
import (
    vault    "github.com/fastabc/fastconf/providers/vault"
    consul   "github.com/fastabc/fastconf/providers/consul"
    httpprov "github.com/fastabc/fastconf/providers/http"
)

vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
```

Trim them out at build time:

```bash
go build -tags no_provider_vault,no_provider_consul,no_provider_http ./...
```

### First-party providers as separate sub-modules

Sub-modules don't ship in the root `go.mod`; `go get` them only when
needed. All implement `contracts.Provider`.

```go
// AWS S3 — load with ETag short-circuit, explicit static credentials.
import s3prov "github.com/fastabc/fastconf/providers/s3"

sp, err := s3prov.New(s3prov.Config{
    Region:    "us-east-1",
    Bucket:    "my-configs",
    Key:       "prod/app.yaml",        // codec inferred from ".yaml"
    AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    // VersionID: "abc...",            // pin to a specific object version
    // Endpoint:  "http://minio:9000", PathStyle: true,  // for MinIO/LocalStack
})
if err != nil {
    log.Fatal(err)
}
mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithProvider(sp))
```

The S3 provider remembers the last ETag and sends `If-None-Match` on
every subsequent `Load`; AWS returns 304 when the object is unchanged
and the provider serves the cached map without re-decoding. That makes
repeated `Reload()` calls cheap and matches the `no-spurious-reload`
contract enforced by `providers/http`.

For "provider address as a config field" patterns, use the URL helper:

```go
cfg, _ := s3prov.FromURL(
    "s3://my-configs/prod/app.yaml?region=us-east-1",
    s3prov.Credentials{
        AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
        SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    },
)
sp, _ := s3prov.New(cfg)
```

`FromURL` accepts `region`, `codec`, `endpoint`, `path_style`,
`version_id`, and `priority` query parameters. Credentials are passed
separately so secrets never appear in URLs that may be logged.

For change-driven reloads, compose with `providers/s3/s3events` (S3 →
EventBridge → SQS):

```go
import (
    s3prov   "github.com/fastabc/fastconf/providers/s3"
    s3events "github.com/fastabc/fastconf/providers/s3/s3events"
)

loader, _ := s3prov.New(s3prov.Config{ /* ... */ })
notifier, _ := s3events.New(s3events.Config{
    Region:    "us-east-1",
    QueueURL:  "https://sqs.us-east-1.amazonaws.com/123/cfg-events",
    Bucket:    "my-configs",
    KeyPrefix: "prod/",                // optional filter
    AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
})

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithProvider(loader),
    fastconf.WithProvider(notifier),   // watch-only; Load returns empty map
)
```

The notifier polls SQS with long-poll, filters EventBridge envelopes by
bucket and key prefix, deletes the matched messages, and emits a
`contracts.Event` that drives a Manager reload. The loader's ETag
short-circuit then makes the re-read free when the event fires for an
unrelated key in the same bucket.

NATS JetStream (`providers/nats`) and Redis Streams (`providers/redisstream`)
are event-driven providers that inject your existing `nats.Conn` / Redis
client adapter through a tiny interface — they pull in no upstream client
library.

### Provider capability matrix

Pick the right module in 30 seconds. "Watch" describes the native
change-notification mechanism; "Resumable" means the provider implements
`contracts.Resumable.WatchFrom` and survives reconnects without losing
events. "Codec" indicates whether the provider needs you to choose one.

| Provider | Module | Watch model | Resumable | Codec | Auth model | Build tag |
|---|---|---|---|---|---|---|
| `pkg/provider.Env` / `EnvReplacer` | root | load-only | — | n/a | env-var prefix | n/a |
| `pkg/provider.CLI` | root | load-only | — | n/a | n/a (in-memory) | n/a |
| `pkg/provider.File` | root | load-only | — | inferred from ext | filesystem | n/a |
| `pkg/provider.Bytes` | root | load-only | — | explicit | n/a (in-memory) | n/a |
| `pkg/provider.DotEnv` | root | load-only | — | n/a | filesystem | n/a |
| `pkg/provider.Labels` / `LabelMap` / `DottedLabels` / `RoutingLabels` | root | load-only | — | n/a | n/a (in-memory) | n/a |
| `providers/http` | root | ETag + body-hash poll | — | required | static headers (Bearer, …) | `no_provider_http` |
| `providers/consul` | root | blocking query (X-Consul-Index) | — | optional (Mode KV/Blob) | ACL token | `no_provider_consul` |
| `providers/vault` | root | metadata-version poll | — | (JSON, built-in) | static token / `WithAuth` | `no_provider_vault` |
| `providers/nats` | root | JetStream subscribe | yes | required | inject `nats.Conn` adapter | — |
| `providers/redisstream` | root | `XREAD BLOCK` | yes | required | inject `redis.Client` adapter | — |
| `providers/s3` | sub-module | load + ETag short-circuit | — | inferred from key ext or explicit | static AWS creds | `no_provider_s3` |
| `providers/s3/s3events` | `providers/s3` subpackage | SQS long-poll (EventBridge) | — | n/a (watch-only) | static AWS creds | `no_provider_s3events` |

Notes:

- *Load-only* providers contribute a layer at every `Reload(ctx)` but do
  not push change events. Pair them with a Manager-level trigger
  (`mgr.Watcher()`, fsnotify, an external scheduler) or a sibling
  event-source provider when you need change-driven reloads.
- *Resumable* providers re-attach from the last observed
  `Event.Revision` on reconnect; non-resumable Watch providers cold-start
  on every reconnect (still correct, just chattier under network churn).
- Build tags strip a provider from the binary entirely; sub-modules
  achieve the same via `go.mod` exclusion (don't `go get` them).

### `contracts.Provider` interface

```go
type Provider interface {
    Name()     string
    Priority() int
    Load(ctx context.Context) (map[string]any, error)
    Watch(ctx context.Context) (<-chan Event, error) // (nil, nil) → no native notifications
}
```

### Priority constants

Merge order follows `Priority()` ascending — higher values overwrite lower:

| Constant | Value | Use |
|---|---:|---|
| `PriorityDotEnv` | 5 | `.env` fallback (lowest) |
| `PriorityStatic` | 10 | Static / file layers |
| `PriorityOverlay` | 20 | Overlay providers |
| `PriorityKV` | 30 | Vault / Consul / HTTP / S3 / NATS / Redis Streams |
| `PriorityK8s` | 40 | Kubernetes ConfigMap / Secret |
| `PriorityEnv` | 50 | Process environment variables |
| `PriorityCLI` | 60 | Command-line flag provider (highest) |

If picking a priority feels arbitrary, use
`WithProviderOrdered(p1, p2, p3)`: each provider receives
`PriorityCLI+100, +101, +102 ...` in call order; later wins. A non-zero
explicit priority on an input is rejected to avoid silent override.

### Resumable (continuation)

```go
type Resumable interface {
    // Empty lastRev acts like Watch (cold subscribe).
    // Non-empty: deliver events strictly after that revision.
    // If the revision was compacted, return ErrResumeUnsupported and the
    // framework falls back to a cold Watch.
    WatchFrom(ctx context.Context, lastRev string) (<-chan Event, error)
}
```

The framework remembers each provider's last observed `Event.Revision`
and passes it back into `WatchFrom` on reconnect.

### Provider factory registry

```go
// Register at init or in TestMain.
fastconf.RegisterProviderFactory("vault", func(cfg map[string]any) (contracts.Provider, error) {
    addr, _ := cfg["addr"].(string)
    path, _ := cfg["path"].(string)
    token, _ := cfg["token"].(string)
    return vault.New(addr, path, token)
})

// Use — provider config can now come from YAML.
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithProviderByName("vault", map[string]any{
        "addr":  "https://vault.svc",
        "path":  "kv/data/myapp",
        "token": os.Getenv("VAULT_TOKEN"),
    }),
)
```

For multi-tenant / per-test isolation use a Manager-local registry:

```go
local := fastconf.NewProviderRegistry()
local.Register("scoped", func(cfg map[string]any) (contracts.Provider, error) {
    return myProvider(cfg)
})

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithProviderRegistry(local),
    fastconf.WithProviderByName("scoped", map[string]any{...}),
)
```

Local registry wins on name collision; global names remain resolvable.

---
