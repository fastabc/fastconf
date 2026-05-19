# FastConf — strongly typed, lock-free, Kustomize-style configuration for Go

> **Language**: English · [中文](README.zh.md)

`fastconf` layers YAML / JSON / TOML files, environment variables, CLI
flags, remote KV stores, and on-the-fly generators into a single strongly
typed Go struct. A single-writer reload loop publishes new snapshots atomically
via `atomic.Pointer`; the hot read path is one `atomic.Pointer.Load()`.

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **Status**: first-public. The API still moves where semantics demand it.
> [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) and this
> README track the current truth of the codebase.

---

## Table of contents

1. [Quick start](#quick-start)
2. [Why FastConf](#why-fastconf)
3. [Installation](#installation)
4. [Core model](#core-model)
5. [Manager API](#manager-api)
6. [Options reference](#options-reference)
7. [Reload pipeline](#reload-pipeline)
8. [Profiles & overlays](#profiles--overlays)
9. [Provider system](#provider-system)
10. [Transformers & migration](#transformers--migration)
11. [Watch, Subscribe, and Plan](#watch-subscribe-and-plan)
12. [Provenance, history & rollback](#provenance-history--rollback)
13. [Observability](#observability)
14. [Multi-tenant & presets](#multi-tenant--presets)
15. [Sub-module ecosystem](#sub-module-ecosystem)
16. [CLI tools](#cli-tools)
17. [Performance](#performance)
18. [Development](#development)
19. [License](#license)

---

## Quick start

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/provider"
)

type AppConfig struct {
    Server struct {
        Addr string `json:"addr" yaml:"addr"`
    } `json:"server" yaml:"server"`
    Database struct {
        DSN  string `json:"dsn"  yaml:"dsn"`
        Pool int    `json:"pool" yaml:"pool"`
    } `json:"database" yaml:"database"`
}

func main() {
    mgr, err := fastconf.New[AppConfig](context.Background(),
        fastconf.WithDir("conf.d"),
        fastconf.WithProfile(fastconf.ProfileOptions{
            EnvVar:  "APP_PROFILE",
            Default: "dev",
        }),
        fastconf.WithProvider(provider.NewEnv("APP_")),
        fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer mgr.Close()

    cfg := mgr.Get() // *AppConfig — lock-free, O(1), zero-alloc
    log.Println(cfg.Server.Addr, cfg.Database.Pool)
}
```

Directory layout:

```text
conf.d/
  base/
    00-app.yaml
  overlays/
    prod/
      50-overrides.yaml
      _patch.json
```

```yaml
# conf.d/base/00-app.yaml
server:
  addr: ":8080"
database:
  dsn: "postgres://localhost/app"
  pool: 10
```

Run with an environment override:

```bash
APP_PROFILE=prod APP_DATABASE_POOL=20 go run .
```

`APP_DATABASE_POOL=20` maps to `database.pool` (single `_` separator,
Viper/Spring Boot style). With `APP_PROFILE=prod`, FastConf merges `base/*`
first, then `overlays/prod/*`.

### Recommended entry points

| Scenario | Recommended combo | Read more | Runnable example |
|---|---|---|---|
| Local file config | `New + WithDir + Get` | [Quickstart](docs/readme/01-quickstart.md) | [`examples/basic`](examples/basic/example_test.go) |
| Kubernetes hot-reload | `PresetK8s + Subscribe + Errors` | [k8s cookbook](docs/cookbook/k8s.md) | [`examples/sidecar`](examples/sidecar/example_test.go) |
| Remote source / GitOps | `WithProvider + Plan + Provenance` | [Vault](docs/cookbook/vault.md) / [Consul](docs/cookbook/consul.md) | [`examples/external_source`](examples/external_source/example_test.go) |

---

## Why FastConf

- **Strong typing on the read path.** `mgr.Get().Server.Addr` is checked
  by the compiler. No dotted-path strings, no reflection, no `interface{}`.
- **Lock-free hot reads.** `Get()` is an `atomic.Pointer.Load()` — O(1),
  zero-alloc, safe from any number of goroutines.
- **Fail-safe reload.** Any pipeline stage that errors out keeps the old
  `*State[T]` live; a broken config never reaches your read path.
- **Kustomize-style layering.** base / overlays, RFC 6902 patches, and
  strategic merge for lists of objects.
- **Opt-in extensions.** Providers, transformers, secret resolvers,
  validators, policies, metrics, and tracing are all optional.

---

## Installation

```bash
go get github.com/fastabc/fastconf@latest

# Optional sub-modules:
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/cue@latest
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/providers/s3@latest
```

Command-line tools (Go ≥ 1.22):

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

### Compatibility

| Item | Supported |
|---|---|
| Go toolchain | 1.22, 1.23, 1.24, 1.25, 1.26 (no toolchain pin in `go.mod`) |
| OS / arch | linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 (binaries published on each tag) |
| Module form | one root module + independent sub-modules (`cue`, `policy/opa`, `validate/playground`, `observability/{otel,metrics/prometheus}`, `providers/s3`, `integrations/{cli/pflag,log/phuslu,log/zerolog}`, `cmd/{fastconfctl,fastconfd,fastconfgen}`) |
| Pre-release contract | semantic-version tags follow `vMAJOR.MINOR.PATCH`. The current line (`v0.18`) is the first public release and the rename / bucketed-Options boundary is locked in — see [migration-v0.18.md](docs/cookbook/migration-v0.18.md). |

### Versioning

- Tags follow `vMAJOR.MINOR.PATCH`. The root module and every sub-module
  receive the same tag through `tools/tag-release.sh vX.Y.Z`.
- Major-version `0` is reserved for the pre-1.0 cycle. Breaking changes
  may still land between minor versions until v1.0, but each release
  ships with an explicit migration recipe under `docs/cookbook/` so the
  call-site delta is mechanical.
- The internal package set under `internal/*` is implementation detail
  and not covered by the SemVer contract — root re-exports (type
  aliases or wrappers) are the only stable surface.
- The reusable primitives under `pkg/*` keep a unidirectional dependency
  shape (see the whitelist in `CLAUDE.md`); `tools/check-deps.sh`
  statically enforces it in CI so consumers can pull in a single
  `pkg/*` subpackage without dragging in hidden lateral dependencies.
- When sub-modules tag independently the tag is module-path-prefixed
  (e.g. `cue/vX.Y.Z`); the README mostly hides this because a single
  release pushes the same version across the root and every sub-module.
- Before each release we run `make test` plus seven guard scripts under
  `tools/{check-layout,check-deps,check-doc-symbols,audit-phase-comments,
  check-cjk-comments,loc-budget,total-loc-budget}.sh`, so directory
  layout, dependency direction, public symbols, comment archaeology and
  LOC budgets are all enforced before a tag is pushed.

---

## Core model

```text
sources / generators / providers
              │
              ▼
       assemble preflight
              │
              ▼
 merge → migration → transform → secret → typed-hooks
      → decode → field-meta → validate → policy
              │
      fail ───┴─── keep old State[T]
              │
           success
              ▼
 canonical hash → atomic swap → history → audit → subscribers
```

| Property | What it means |
|---|---|
| Typed read path | `mgr.Get().Server.Addr`, checked by the compiler |
| Single-writer reload | fsnotify, provider events, and manual `Reload` all serialize through one writer |
| Fail-safe | Any stage error keeps the old `*State[T]`; bad config never reaches business code |
| Kustomize-style layering | base / overlay, RFC 6902 patches, strategic merge with `mergeKeys` |
| Opt-in extensions | providers, transformers, secret resolvers, policies, metrics, tracer |

---

## Manager API

```go
// Construction (first reload runs synchronously)
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error)

// Read path — lock-free, O(1), zero-alloc
func (m *Manager[T]) Get() *T

// Trigger a reload; ctx controls the full pipeline.
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error

// Dry-run — never updates the live pointer
func (m *Manager[T]) Plan() *PlanBuilder[T]

// Current snapshot (State[T] + Sources + Origins)
func (m *Manager[T]) Snapshot() *State[T]

// Async failure stream — buffered 16, drop-on-full, closed by Close()
func (m *Manager[T]) Errors() <-chan ReloadError

func (m *Manager[T]) Watcher() *Watcher[T]  // .Pause() / .Resume()
func (m *Manager[T]) Replay()  *Replay[T]   // .List() / .Rollback(*State[T])
func (m *Manager[T]) Close() error
```

Package-level generics:

```go
// Per-field subscribe; fires on every successful reload.
func Subscribe[T, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M)) (cancel func())

// Typed feature-flag evaluation.
func Eval[T, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V
```

`State[T]` carries `Value *T`, `Hash [32]byte`, `Generation uint64`,
`Sources []SourceRef`, and provenance helpers (`Explain`, `Diff`, `Redacted`).

---

## Options reference

All `WithXxx` options return `Option` and may be composed in any order.
The full reference is in [docs/readme/02-core-model.md](docs/readme/02-core-model.md).

### Key options

| Option | Purpose | Default |
|---|---|---|
| `WithDir(dir)` | Config root directory | `"conf.d"` |
| `WithFS(fs.FS)` | Alternate `fs.FS` (testing) | — |
| `WithWatch(WatchOptions{...})` | Enable fsnotify; bundles `Enabled` / `Paths` / `Coalesce` / `CoalesceProfile` | `Enabled:false` |
| `WithProfile(ProfileOptions{...})` | Profile selection bundle: `Single`, `Multi`, `Expr`, `EnvVar`, `Default` | — |
| `WithCoalesce(CoalesceOptions{...})` | Tune watcher `Quiet` / `MaxLag` / `SwapHint` independently of `WithWatch` | — |
| `WithProvider(p)` | Register a structured provider | — |
| `WithSource(src, parser)` | Byte-blob source + parser | — |
| `WithMigrations(fn)` | Schema migration callback | — |
| `WithTransformers(t...)` | Post-merge transform chain | — |
| `WithSecretResolver(r)` | Decrypt leaves before decode | — |
| `WithValidator[T](fn)` | Typed validation after decode | — |
| `WithPolicy[T](p)` | Policy evaluation after validate | — |
| `WithHistory(n)` | Keep last `n` successful states | — |
| `WithProvenance(level)` | `Off` / `TopLevel` / `Full` | `Off` |
| `WithMetrics(sink)` | Metrics sink | — |
| `WithAuditSink(sink)` | Audit on each successful reload | — |
| `WithTracer(tracer)` | OTel-compatible tracer | — |
| `WithLogger(*slog.Logger)` | Inject a logger | `io.Discard` |
| `WithStructDefaults[T]()` | Populate zero values via `fastconf:"default=…"` tags | — |

---

## Reload pipeline

### Stage sequence

```
reloadCh.recv(req)
  ├─ stageMerge:      discovery.Scan(dir) → decode files → merger.Merge(layers)
  │                   apply _meta.yaml (appendSlices / profileEnv / match)
  │                   apply _patch.json (RFC 6902)
  ├─ stageAssemble:   for each provider: Load(ctx) → merge by Priority
  ├─ stageMigrate:    opts.migrationRun(merged)
  ├─ stageTransform:  for each transformer: t.Transform(merged)
  ├─ stageDecode:     json.Marshal(merged) → json.Unmarshal(→ *T)
  ├─ stageFieldMeta:  range / enum / required checks
  ├─ stageValidate:   for each validator: v(*T)
  ├─ stagePolicy:     for each policy: p.Evaluate(ctx, *T, reason, tenant)
  └─ commit:
       canonical SHA-256 dedup
       atomic.Pointer.Store(newState) → history → audit → subscribers
```

When any stage errors: `atomic.Pointer` is **not** updated, `Generation`
is **not** incremented, the error surfaces on `Errors()`, no `AuditSink` fires.

---

## Profiles & overlays

```text
conf.d/
  base/                     # applied for every profile
    00-defaults.yaml
  overlays/
    prod/
      50-prod.yaml
      _meta.yaml            # profile match expression
      _patch.json           # RFC 6902 patch
```

### `_meta.yaml`

```yaml
schemaVersion: "1"
profileEnv: "APP_PROFILE"
defaultProfile: "dev"
appendSlices: true
match: "prod | staging"     # &, |, !, () supported
```

### RFC 6902 JSON Patch

```json
[
  { "op": "replace", "path": "/server/addr",      "value": ":8443" },
  { "op": "add",     "path": "/feature/darkMode", "value": true    },
  { "op": "remove",  "path": "/legacy/key"                         }
]
```

Multi-profile mode: `WithProfile(ProfileOptions{Multi: []string{"prod", "eu-west", "canary"}})`
— each overlay's `_meta.yaml.match` decides whether it applies.

---

## Provider system

### Built-in structured providers (`pkg/provider`)

| Provider | Constructor | Notes |
|---|---|---|
| Env | `provider.NewEnv("APP_")` | `APP_FOO_BAR` → `foo.bar`; chain `.WithReplacer`, `.At`, `.WithCoerce` |
| CLI | `provider.NewCLI(map)` | Pass only explicitly changed flags; files/env stay authoritative |
| DotEnv | `provider.NewDotEnv("APP_", paths...)` | `.env` fallback; process env wins |
| Labels | `provider.NewDottedLabels(labels, opts)` / `NewRoutingLabels(labels, opts)` | Config and routing DSL labels |
| K8s Downward | `k8s.NewDefault()` | `/etc/podinfo/{labels,annotations}` |

First-party KV providers (root module, trim via build tag):

```go
vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
// Build tag to exclude: -tags no_provider_vault,no_provider_consul,no_provider_http
```

Sub-module providers (`go get` as needed): S3 (`providers/s3`), NATS
(`providers/nats`), Redis Streams (`providers/redisstream`).

### Priority constants

Merge order follows `Priority()` ascending — higher values overwrite lower:

| Constant | Value | Use |
|---|---:|---|
| `PriorityDotEnv` | 5 | `.env` fallback |
| `PriorityStatic` | 10 | Static / file layers |
| `PriorityKV` | 30 | Vault / Consul / HTTP / S3 |
| `PriorityK8s` | 40 | Kubernetes ConfigMap / Secret |
| `PriorityEnv` | 50 | Process environment variables |
| `PriorityCLI` | 60 | Command-line flags (highest) |

Use `WithProviderOrdered(p1, p2, p3)` to auto-assign priorities in call order.

### `contracts.Provider` interface

```go
type Provider interface {
    Name()     string
    Priority() int
    Load(ctx context.Context) (map[string]any, error)
    Watch(ctx context.Context) (<-chan Event, error)
}
```

---

## Transformers & migration

### Built-in transformers (`pkg/transform`)

```go
fastconf.WithTransformers(
    transform.Defaults(map[string]any{"server": map[string]any{"timeout": "30s"}}),
    transform.SetIfAbsent("server.timeout", "30s"),
    transform.EnvSubst(),                           // ${VAR} / ${VAR:-default}
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{"db.url": "database.dsn"}),
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
```

### Migration

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

For multi-step schema migrations use `pkg/migration.NewChain`.

---

## Watch, Subscribe, and Plan

### Field-level Subscribe

```go
cancel := fastconf.Subscribe(mgr,
    func(app *AppConfig) *DatabaseConfig { return &app.Database },
    func(old, neu *DatabaseConfig) {
        reconnect(neu.DSN)
    },
)
defer cancel()
```

### Manual reload with one-shot override

```go
err := mgr.Reload(ctx,
    fastconf.WithReloadReason("admin-cli"),
    fastconf.WithSourceOverride(map[string]any{
        "server": map[string]any{"addr": ":9999"},
    }),
)
```

### Plan (dry-run)

```go
result, err := mgr.Plan().WithHostname("ci-runner-7").Run(ctx)
// result.Validators — validation errors
// result.Policies   — policy violations (SeverityError downgraded to warning in dry-run)
```

### Pause / Resume

```go
mgr.Watcher().Pause()
applyBatchUpdate()
mgr.Watcher().Resume()
```

---

## Provenance, history & rollback

### Provenance

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvenance(fastconf.ProvenanceFull),
)

origins := mgr.Snapshot().Explain("server.addr")
// each Origin: Source.Name, Source.Priority, Value
```

| Level | Cost | What you can trace |
|---|---|---|
| `ProvenanceOff` | zero | nothing |
| `ProvenanceTopLevel` | O(top-level keys) | which layer set each top-level field |
| `ProvenanceFull` | O(leaves) | full override chain per leaf |

### History & rollback

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithHistory(10),
)
history := mgr.Replay().List()     // []*State[T], oldest → newest
_ = mgr.Replay().Rollback(history[len(history)-2])
```

### Errors stream

```go
go func() {
    for re := range mgr.Errors() {
        slog.Error("reload failed", "reason", re.Reason, "err", re.Err)
    }
}()
```

---

## Observability

```go
// JSON-lines audit on each successful reload
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithAuditSink(fastconf.NewJSONAuditSink(os.Stderr)),
    fastconf.WithDiffReporter(fastconf.DiffReporterFunc(
        func(ctx context.Context, ev fastconf.DiffEvent) error {
            return slack.Post(ctx, ev.Diff) // async, never blocks reload
        },
    )),
)
```

Prometheus metrics and OpenTelemetry tracing live in sub-modules:

```go
import prommetrics "github.com/fastabc/fastconf/observability/metrics/prometheus"
import fastconfotel "github.com/fastabc/fastconf/observability/otel"

fastconf.WithMetrics(prommetrics.New())
fastconf.WithTracer(fastconfotel.NewTracer(otel.GetTracerProvider()))
```

Policy violations abort reload at `SeverityError`; `SeverityWarning` logs
and continues. CUE and OPA implementations in `cue/policy` and `policy/opa`.

---

## Multi-tenant & presets

```go
// Multi-tenant: each tenant is a fully isolated Manager[T]
tm := fastconf.NewTenantManager[AppConfig]()
mgrA, _ := tm.Add(ctx, "tenant-a", fastconf.WithDir("/etc/config/tenant-a"))
app, err := tm.Get("tenant-a")  // fastconf.ErrUnknownTenant if absent
tm.Close()
```

```go
// Presets
fastconf.PresetK8s(fastconf.K8sOpts{Dir: "/etc/config", Watch: true})
fastconf.PresetSidecar(fastconf.SidecarOpts{Dir: "/etc/fastconfd", HistoryN: 16})
fastconf.PresetTesting(fastconf.TestingOpts{FS: memFS, Profile: "testing"})
```

---

## Sub-module ecosystem

### Shipped with the root module

| Package | Path |
|---|---|
| contracts | `contracts` — public interfaces |
| reusable primitives | `pkg/{decoder,discovery,feature,flog,generator,merger,migration,provider,transform,validate}` |
| http / vault / consul | `providers/{http,vault,consul}` — build tags: `no_provider_{http,vault,consul}` |
| policy | `policy` — `Func` adapter |
| sidecar service | `cmd/fastconfd` |

### Independent sub-modules (`go get` as needed)

| Sub-module | Path | Primary dependency |
|---|---|---|
| validate/playground | `validate/playground` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | prometheus/client_golang |
| otel | `observability/otel` | OpenTelemetry SDK |
| cue (validation + policy) | `cue` | cuelang.org/go |
| opa-policy | `policy/opa` | open-policy-agent/opa |
| cli/pflag | `integrations/cli/pflag` | spf13/pflag |
| nats provider | `providers/nats` | root module (inject `nats.Conn`) |
| redis-streams provider | `providers/redisstream` | root module (inject redis client) |
| s3 provider | `providers/s3` | AWS SDK v2 |
| openfeature | `integrations/openfeature` | root module |
| fastconfctl | `cmd/fastconfctl` | root module |
| fastconfgen | `cmd/fastconfgen` | yaml.v3 |

Tag all sub-modules at once: `./tools/tag-release.sh vX.Y.Z [--push]`

---

## CLI tools

### `fastconfd` — sidecar service

```bash
fastconfd --dir=/etc/config --profile=prod --addr=:8081
```

| Endpoint | Method | Description |
|---|---|---|
| `/healthz` | GET  | `{"status":"ok","generation":N}` |
| `/config`  | GET  | Current config JSON (secrets redacted) |
| `/reload`  | POST | Trigger a manual reload |
| `/events`  | GET  | SSE stream of `ReloadCause` on each successful reload |

### `fastconfctl` — admin CLI

```bash
fastconfctl snapshot --addr=:8081
fastconfctl reload   --addr=:8081 --request-id=deploy-123
fastconfctl rollback --addr=:8081 --generation=42
```

### `fastconfgen` — code generator

```bash
fastconfgen generate --input=conf.d/base/00-app.yaml --pkg=config --out=config/config_gen.go
```

---

## Performance

Most recent benchmark run: **Apple M2 / darwin-arm64 / Go 1.26.2**.

| Benchmark | median |
|---|---:|
| `BenchmarkGet` | 0.52 ns/op |
| `BenchmarkReloadNoop` | 15.1 µs/op |
| `BenchmarkReloadCommitSmall` | 16.5 µs/op |
| `BenchmarkReloadManySubscribers/50` | 17.5 µs/op |

Full baseline: [`docs/design/perf.md`](docs/design/perf.md).

---

## Development

```bash
go mod tidy
make build
make test        # go test -race -count=1 ./...
make test-all    # includes sub-modules
make lint        # requires golangci-lint

go test ./... -run '^Example' -v
go test -bench=BenchmarkGet -benchmem ./...
```

---

## Documentation

| Doc | Purpose |
|---|---|
| [docs/readme/](docs/readme/) | In-depth chapters: core model, pipeline, extensions, operations |
| [docs/cookbook/README.md](docs/cookbook/README.md) | Ready recipes ordered by user journey |
| [docs/design/spec.md](docs/design/spec.md) | Runtime model, concurrency, module boundaries |
| [docs/cookbook/migration-v0.18.md](docs/cookbook/migration-v0.18.md) | v0.18 rename / bucketed-Options migration table |
| [GitHub Releases](https://github.com/fastabc/fastconf/releases) | Release notes and prebuilt CLI binaries |
| [pkg.go.dev](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc and runnable examples |

Common recipes: [k8s](docs/cookbook/k8s.md) · [vault](docs/cookbook/vault.md) ·
[consul](docs/cookbook/consul.md) · [secrets](docs/cookbook/secrets.md) ·
[features](docs/cookbook/features.md) · [policy](docs/cookbook/policy.md) ·
[otel](docs/cookbook/otel.md) · [tenant](docs/cookbook/tenant.md) ·
[sidecar](docs/cookbook/sidecar.md) · [plan](docs/cookbook/plan.md)

---

## License

MIT License, See [`LICENSE`](LICENSE).

Copyright (c) 2026 FastAbc
