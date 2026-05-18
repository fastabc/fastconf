# FastConf — strongly typed, lock-free, Kustomize-style configuration for Go

> **Language**: English · [中文](README.zh.md)

`fastconf` layers YAML / JSON / TOML files, environment variables, CLI
flags, remote KV stores, and on-the-fly generators into a single strongly
typed Go struct. A single-writer reload loop publishes new snapshots atomically
via `atomic.Pointer`; the hot read path is one `atomic.Pointer.Load()`.

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **Status**: pre-public. The API still moves where semantics demand it.
> [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) and this
> README track the current truth of the codebase.

---

## Table of contents

1. [Quick start](#quick-start)
2. [Why FastConf](#why-fastconf)
3. [Coming from another config library](#coming-from-another-config-library)
4. [Installation](#installation)
5. [Core model](#core-model)
6. [Manager API](#manager-api)
7. [Options reference](#options-reference)
8. [Reload pipeline](#reload-pipeline)
9. [Profiles & overlays](#profiles--overlays)
10. [Provider system](#provider-system)
11. [Codec & bridge](#codec--bridge)
12. [Transformers & migration](#transformers--migration)
13. [Watch, Subscribe, and Plan](#watch-subscribe-and-plan)
14. [Provenance, history & rollback](#provenance-history--rollback)
15. [Observability](#observability)
16. [Multi-tenant & presets](#multi-tenant--presets)
17. [Sub-module ecosystem](#sub-module-ecosystem)
18. [Extension guide](#extension-guide)
19. [CLI tools](#cli-tools)
20. [Performance](#performance)
21. [Development](#development)
22. [License](#license)

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
        fastconf.WithProfileEnv("APP_PROFILE"),
        fastconf.WithDefaultProfile("dev"),
        fastconf.WithProvider(provider.NewEnv("APP_")),
        fastconf.WithWatch(true),
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

Example base file:

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

`APP_DATABASE_POOL=20` maps to `database.pool` (single `_` is the default
separator, Viper / Spring Boot style — switch to `__` via
`provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)`
when keys must carry literal underscores). The external label
`server.addr=:9090` maps to `server.addr`. With the example above, env
overrides the file value for `database.pool`, and labels override the file
value for `server.addr`.

With `APP_PROFILE=prod`, FastConf merges `base/*` first, then
`overlays/prod/*`. The default decode bridge does a JSON round-trip, so if
your structs only carry `yaml` tags either add `json` tags or pass
`fastconf.WithCodecBridge(fastconf.BridgeYAML)` explicitly.

### Three recommended entry points

| Scenario | Recommended combo | Read next |
|---|---|---|
| Local file config, single service | `New + WithDir + Get` | `ExampleNew` / `docs/cookbook/introspect.md` |
| Kubernetes hot-reload | `PresetK8s + Subscribe + Errors` | `docs/cookbook/k8s.md` / `docs/cookbook/reload-policy.md` |
| Remote source / GitOps | `WithProvider + Plan + Provenance` | `docs/cookbook/vault.md` / `docs/cookbook/consul.md` / `docs/cookbook/plan.md` |

For unit tests use `PresetTesting`; for sidecars `PresetSidecar`; for
region / zone / host axis overlays see `PresetHierarchical` and
`WithMultiAxisOverlays`.

---

## Why FastConf

- **Strong typing on the read path.** `mgr.Get().Server.Addr` is checked
  by the compiler. No dotted-path strings, no reflection, no `interface{}`.
- **Lock-free hot reads.** `Get()` is an `atomic.Pointer.Load()` — O(1),
  zero-alloc, safe from any number of goroutines.
- **Fail-safe reload.** Any pipeline stage that errors out keeps the old
  `*State[T]` live; a broken config never reaches your read path.
- **Kustomize-style layering.** base / overlays, RFC 6902 patches, and
  policy-based `mergeKeys` strategic merge for lists of objects.
- **Opt-in extensions.** Providers, transformers, secret resolvers,
  validators, policies, metrics, and tracing are all optional.
- **Boundary-honest interface surface.** Public contracts live under
  `contracts/`; reusable primitives live under `pkg/*`; private helpers
  under `internal/*`; CI enforces dependency direction.

---

## Coming from another config library

Quick translation table for the most common idioms.

| Your library | Their idiom | FastConf equivalent | Caveat |
|---|---|---|---|
| **spf13/viper** | `viper.BindPFlag(...)` | `provider.NewCLIChanged(cliadapter_pflag.FromChanged(cmd.Flags()))` | `BindPFlag` leaks pflag **defaults** into config; FastConf only forwards flags whose `Changed` bit is set. |
| **spf13/viper** | precedence (override > flag > env > config > kv > default) | `Priority*` constants: `PriorityDotEnv=5` → `PriorityCLI=60`, 7 explicit bands | DotEnv and K8s are first-class bands; precedence is set per-provider, not globally. |
| **knadh/koanf** | `k.Load(provider, parser)` — last load wins | `mgr.Add(provider)` + each provider's `Priority()` | Load order is **irrelevant**; priority alone decides. Reorder freely. |
| **knadh/koanf** | `koanf.WithMergeFunc(...)` | `pkg/merger` strategy + `policy/*` sub-modules | Strategy-driven merge (RFC 6902, mergeKeys, etc.), configured via options. |
| **kelseyhightower/envconfig** | `envconfig.Process("APP", &cfg)` | `provider.NewEnv("APP_")` | Prefix-based provider, not struct-tag scanner. CamelCase auto-split (`split_words`) is **not** supported — write the dotted key. |
| **kelseyhightower/envconfig** | `default:"foo"` tag | `merger.Defaults` layer (or struct zero value) | Defaults live in a dedicated layer, not in tags. |
| **kelseyhightower/envconfig** | `required:"true"` tag | `pkg/validate.Required(...)` | Validation is its own pipeline stage; runs after merge. |
| **caarlos0/env** | `envExpand` (`${VAR}` interpolation) | `transform.EnvSubst()` (process env) or `transform.EnvSubstWith(lookup func(string) string)` (custom) | Explicit transformer; supply a lookup closure to consult dotenv before `os.Getenv`. |
| **joho/godotenv** | `godotenv.Load(".env")` | `provider.NewDotEnv("APP_", ".env")` at `PriorityDotEnv=5` | **No `os.Setenv` mutation** — `.env` is a layer, not a side effect. Process env still overrides (presence-based, so `APP_PORT=""` also suppresses). |
| **joho/godotenv** | `godotenv.Overload(".env")` (force override) | `provider.NewDotEnv(...).WithPriority(contracts.PriorityCLI)` | Priority knob replaces the dual API. |
| **spf13/cobra + pflag** | `cmd.Flags()` | `cliadapter_pflag.FromChanged(cmd.Flags())` → `provider.NewCLIChanged(...)` | Sub-module `github.com/fastabc/fastconf/integrations/cli/pflag` — keeps pflag out of the root module's dependency closure. |
| **stdlib `flag`** | `flag.FlagSet` | `cliadapter.FromStdFlag(fs)` → `provider.NewCLIChanged(...)` | Zero-dep; lives in `pkg/cliadapter`. |
| **alecthomas/kong** / **urfave/cli** | typed flag struct / `cli.Context` | use `cliadapter.From(visit)` with a one-line visit closure | Pattern: walk only `Changed` / `IsSet` flags and call `yield(name, value)`. |

### Side-by-side: flag binding without the default-leak footgun

The single most common Viper bug is `BindPFlag` happily forwarding the
flag's **default** value into config even when the user never typed the
flag — silently overriding values you set in YAML or env. FastConf splits
the two concerns:

```go
// Viper (footgun-prone):
//   pflag default ("8080") wins over server.port: 9090 in app.yaml
viper.BindPFlag("server.port", cmd.Flags().Lookup("server.port"))

// FastConf (changed-only by construction):
//   only set if --server.port was explicitly typed
import cliflag "github.com/fastabc/fastconf/integrations/cli/pflag"

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(provider.NewCLIChanged(cliflag.FromChanged(cmd.Flags()))),
)
```

### Side-by-side: env binding without struct-tag scanning

```go
// envconfig (struct-tag scanner, one-shot):
type Cfg struct {
    DSN  string `envconfig:"DATABASE_DSN" required:"true" default:"sqlite:///tmp/db"`
    Port int    `envconfig:"SERVER_PORT"  default:"8080"`
}
_ = envconfig.Process("APP", &cfg)

// FastConf (provider layer + dedicated defaults & validate):
type Cfg struct {
    Database struct{ DSN  string } // populated by env: APP_DATABASE_DSN
    Server   struct{ Port int }    // populated by env: APP_SERVER_PORT
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDefaults(Cfg{ /* zero-value or filled defaults */ }),
    fastconf.WithProvider(provider.NewEnv("APP_")),    // _ → . relaxed binding
    fastconf.WithValidate(validate.Required("Database.DSN")),
)
```

---

## Installation

```bash
go get github.com/fastabc/fastconf@latest

# Optional sub-modules:
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/cue@latest           # CUE validation + policy
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/providers/s3@latest
go get github.com/fastabc/fastconf/validate/playground@latest
```

Command-line tools (Go ≥ 1.26):

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

Each GitHub Release also ships prebuilt binaries for
`linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and `windows/amd64` with
`SHA256SUMS`.

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

### Source layout

```
.                       (repo root — package fastconf)
  manager.go            Manager[T]: New / Get / Close / Reload / Snapshot
  pipeline.go           runStages[T] + Plan dry-run entry
  pipeline_stages.go    Merge / Assemble / Migrate / Transform / Decode / Validate stages
  options.go            All WithXxx options + public types
  state.go              State[T] + ReloadCause + Origins/Explain/Lookup + history
  watch.go / watcher.go Subscribe, fsnotify, symlink handling
  provider_watch.go     Provider event subscription (exponential backoff + drop-on-full)
  presets.go            PresetK8s / PresetSidecar / PresetTesting / PresetHierarchical
  registry.go           RegisterProviderFactory / WithProviderByName
  defaults.go           fastconf:"default=…" struct tag + built-in hooks
  secret.go             fastconf:"secret" + SecretRedactor
  feature.go            FeatureRule / Eval / Sub
  field_meta.go         range / enum / required field-meta checks
  obs_audit.go / obs_metrics.go / obs_tracer.go   sinks
  tenant.go             TenantManager[T]

contracts/              Public stable interfaces: Provider / Codec / Event / Snapshot / Source / Priority
pkg/                    Reusable primitives — importable by third-party authors
  decoder/              YAML / JSON / TOML codec registry
  discovery/            conf.d scanning + _meta.yaml parsing
  feature/              feature-flag rule + EvalContext
  flog/                 zerolog-style fluent wrapper over *slog.Logger
  generator/            contracts.Generator helpers
  mappath/              dotted-path Get/Set/Delete utilities
  merger/               Kustomize-style map[string]any layering
  migration/            Chain + Step (From/To/Apply)
  profile/              profile expression compiler (&, |, !, ())
  provider/             built-in Env / CLI / Bytes / File / Labels providers
  transform/            Defaults / SetIfAbsent / EnvSubst / DeletePaths / Aliases
  validate/             Validator + ValidatorReport
internal/               Private helpers (debounce / obs / typeinfo / watcher)
providers/              First-party providers (consul / http / vault / nats / redisstream / k8s in root module; s3 + s3/s3events as sub-module)
integrations/           bus / render / log / openfeature adapters
observability/          metrics/prometheus + otel (independent sub-modules)
policy/                 Policy interface; opa backend as sub-module; cue backend in cue/ module
cue/                    Unified CUE sub-module: cue/cuelang (validation) + cue/policy (policy backend)
validate/               playground (independent sub-module)
cmd/                    fastconfd (root module); fastconfctl / fastconfgen (sub-modules)
```

### Dependency direction (CI-enforced)

```
fastconf  →  pkg/{discovery,decoder,flog,merger,provider,validate}
          →  internal/watcher
          →  contracts

pkg/* MUST NOT depend on each other except via this whitelist
(kept in sync with tools/check-deps.sh):
  pkg/discovery → pkg/profile
  pkg/generator → pkg/mappath
  pkg/provider  → pkg/decoder
  pkg/provider  → pkg/mappath
  pkg/transform → pkg/mappath
internal/* MUST NOT depend on each other; only the standard library.
```

---

## Manager API

```go
type Manager[T any] struct { /* unexported */ }

// Construction (first reload runs synchronously)
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error)

// Read path — lock-free, O(1), zero-alloc
func (m *Manager[T]) Get() *T

// Write path. ctx controls both enqueue/wait AND the pipeline itself:
// cancelling it aborts provider.Load / secret resolvers / transformers
// and surfaces as ctx.Err().
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error

// Dry-run — never updates the live pointer; collects every ValidatorReport
func (m *Manager[T]) Plan() *PlanBuilder[T] // .WithHostname(...).Run(ctx) → *PlanResult[T]

// Current snapshot (State[T] + Sources + Origins)
func (m *Manager[T]) Snapshot() *State[T]

// Async failure stream — buffered 16, drop-on-full, closed by Close()
func (m *Manager[T]) Errors() <-chan ReloadError

// Sub-system accessors (zero-cost namespaces)
func (m *Manager[T]) Watcher() *Watcher[T]  // .Pause() / .Resume() / .Paused()
func (m *Manager[T]) Replay()  *Replay[T]   // .List() / .Rollback(*State[T])

func (m *Manager[T]) Close() error
```

Package-level generics — anything that derives a subtree `M` from `*T`
lives at the package level:

```go
// Per-field subscribe; fires on every successful reload.
func Subscribe[T, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M)) (cancel func())

// Typed feature-flag evaluation; type-mismatch returns def.
func Eval[T, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V

// Read-only subtree alias.
func Sub[T, M any](s *State[T], extract func(*T) *M) *M
```

### `State[T]` — immutable snapshot

```go
type State[T any] struct {
    Value      *T             // strongly typed config; Get() returns this
    Hash       [32]byte       // global SHA-256 fingerprint
    LoadedAt   int64          // unix nanoseconds
    Sources    []SourceRef    // every layer that contributed
    Generation uint64         // monotonic version
    Cause      ReloadCause    // why this reload ran + provider revisions
}

func (s *State[T]) Explain(path string) []Origin             // oldest → newest override chain
func (s *State[T]) Lookup(path string) []Origin              // alias of Explain
func (s *State[T]) LookupStrict(path string) ([]Origin, error)
func (s *State[T]) Origins() *OriginIndex
func (s *State[T]) Introspect() *Introspection               // Keys / Settings / At
func (s *State[T]) Redacted() map[string]any                 // applies the SecretRedactor
func (s *State[T]) MarshalYAML(redactor SecretRedactor) ([]byte, error)
func (s *State[T]) Diff(other *State[T]) []string
func (s *State[T]) FeatureRules() map[string]feature.Rule
```

Suggested reading order on pkg.go.dev:
`New` → `Get` → `Subscribe` / `Errors` → `Plan` → `Replay`. Runnable
examples: `ExampleNew`, `ExampleSubscribe`, `ExampleManager_Errors`,
`ExampleManager_Plan`, `ExampleReplay_Rollback`.

---

## Options reference

All `WithXxx` options return `Option` and may be composed in any order
when passed to `New[T]`. Later calls win for duplicates.

### Filesystem

| Option | Purpose | Default |
|---|---|---|
| `WithDir(dir string)` | Config root directory | `"conf.d"` |
| `WithFS(fs.FS)` | Alternate `fs.FS` (testing) | — |
| `WithStrict(bool)` | Error on unknown fields | `false` |
| `WithLogger(*slog.Logger)` | Inject a logger | `io.Discard` (opt-in) |
| `WithCodecBridge(BridgeJSON \| BridgeYAML)` | Decode bridge | `BridgeJSON` |
| `WithMultiAxisOverlays(axes ...OverlayAxis)` | Multi-axis overlays (region / zone / host) | — |
| `WithRawMapAccess(fn)` | Read-only hook over the merged map before decode | — |

### Watch

| Option | Purpose | Default |
|---|---|---|
| `WithWatch(bool)` | Enable fsnotify | `false` |
| `WithCoalesceQuiet(d)` | Quiet window after which a per-dir burst fires | `30ms` |
| `WithCoalesceMaxLag(d)` | Hard upper bound on burst lifetime | `250ms` |
| `WithCoalesceSwapHint(d)` | Tightened window once a K8s `..data` swap is detected | `5ms` |
| `WithCoalesceProfile(p)` | Apply a preset: `ProfileK8s` (default) or `ProfileLocalDev` | `ProfileK8s` |
| `WithWatchPaths(paths...)` | Additional watch paths | — |

The watcher debounces fsnotify events per **parent directory** rather than
globally, so independent ConfigMaps (or watched dirs) never block each
other. When a K8s atomic-swap commit (`..data` rename/create) is observed,
the coalescer tightens the window to `swapHint` (5ms) instead of waiting
the full `quiet` window — typical reload latency drops from ~500ms (the
prior global debouncer default) to ~5–35ms.

### Profile

| Option | Purpose |
|---|---|
| `WithProfile(p string)` | Explicit single profile |
| `WithProfiles(p ...string)` | Multi-profile mode (overlays match via `_meta.yaml.match`) |
| `WithProfileEnv(name string)` | Read profile from an environment variable |
| `WithDefaultProfile(p string)` | Fallback when the env var is empty |
| `WithProfileExpr(expr string)` | Global profile-matching expression |

### Source × Parser × Provider

FastConf splits the extension surface in two:

- **`Source`** (`pkg/source`) — a byte-stream contributor (file, http,
  inline bytes). Paired with a **`Parser`** (`pkg/parser`) at the call
  site, koanf-style, so the codec is named where the layer is declared.
- **`Provider`** (`pkg/provider`) — an already-structured contributor
  (env, cli, KV with one key per setting). No Parser needed.

| Option | Purpose |
|---|---|
| `WithSource(src, parser)` | Bind a byte-blob Source with a Parser. Pass `nil` Parser to auto-pick via content-type hint |
| `WithProvider(p)` | Register an already-structured provider |
| `WithProviderOrdered(p...)` | Auto-assigns `CLI+100, +101, ...` in call order; errors if input has non-zero priority |
| `WithProviderByName(name, cfg)` | Construct via factory registry (resolved after all options applied) |
| `WithProviderRegistry(r)` | Manager-local `*ProviderRegistry` — local wins, then global default |
| `WithGenerator(g)` | Synthesise a `[]RawLayer` in the assemble stage (e.g. BuildInfo) |
| `WithDotEnvAuto(prefix)` | Auto-discover a `.env` file under `WithDir` |

`pkg/source` and `pkg/parser` factory functions:

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/parser"
    "github.com/fastabc/fastconf/pkg/provider"
    "github.com/fastabc/fastconf/pkg/source"
    "github.com/fastabc/fastconf/pkg/transform"
)

fastconf.New[Cfg](ctx,
    // Byte-blob layers — explicit Source × Parser pairing:
    fastconf.WithSource(source.NewFile("/etc/app/config.yaml"), parser.YAML()),
    fastconf.WithSource(source.NewHTTP("https://kv/config"), parser.JSON()),
    fastconf.WithSource(source.NewBytes("inline", "yaml", data), nil), // nil = auto-bind by content-type

    // Structured providers — no Parser slot:
    fastconf.WithProvider(provider.NewEnv("APP_")),                              // APP_DATABASE_DSN → database.dsn (default DotReplacer)
    fastconf.WithProvider(provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)), // preserves single "_", splits on "__"
    fastconf.WithProvider(provider.NewEnv("APP_").At("config.runtime")),         // graft env tree under a sub-path
    fastconf.WithProvider(provider.NewCLIChanged(cliMap)),                       // explicit CLI overrides only
    fastconf.WithProvider(provider.NewDotEnv("APP_", ".env")),                   // explicit .env fallback paths
    fastconf.WithProvider(provider.NewDottedLabels(labels, provider.DottedLabelOptions{})), // explicit dotted config labels
    fastconf.WithProvider(provider.NewRoutingLabels(labels, provider.RoutingLabelOptions{})), // routing DSL labels (typed/list/index semantics)
    fastconf.WithTransformers(transform.ExpandLabels(at, to, opts)),
)
```

### Pipeline enhancers

| Option | Purpose |
|---|---|
| `WithMigrations(func)` | Schema migration callback (before transformers) |
| `WithTransformers(t...)` | Post-merge, pre-decode transformation chain |
| `WithSecretResolver(r)` | Decrypt leaf secrets after transform, before decode |
| `WithTypedHook(h)` | Rewrite leaves before decode (built-in: `time.Duration`) |
| `WithoutDefaultTypedHooks()` | Disable built-in typed hooks |
| `WithStructDefaults[T]()` | Populate zero values via `fastconf:"default=..."` |
| `WithDefaulterFunc[T](fn)` | Custom defaulter for `*T` |
| `WithMergeKeys(map)` | Strategic merge for lists of objects |
| `WithValidator[T](fn)` | Typed validation after decode; failure preserves old state |
| `WithPolicy[T](p)` | Policy evaluation after validate; `SeverityError` aborts reload |
| `WithFeatureRules[T](extract)` | Attach a `feature.Rule` table to State for `Eval` |

### Observability

| Option | Purpose |
|---|---|
| `WithMetrics(MetricsSink)` | Metrics sink (also supports `ProviderMetricsSink` / `StageMetricsSink` / `RenderMetricsSink`) |
| `WithAuditSink(AuditSink)` | Callback on every successful reload (multi-sink fan-out) |
| `WithDiffReporter(DiffReporter)` | Async push on non-empty diff; each reporter has its own bounded worker; drop-on-full emits `EventDropped("diff-reporter")` |
| `WithDiffReporterQueueCap(n int)` | Per-reporter queue depth (default 64) |
| `WithTracer(Tracer)` | OTel-compatible span tracer |
| `WithProvenance(level)` | `ProvenanceOff` / `ProvenanceTopLevel` / `ProvenanceFull` |
| `WithHistory(n)` | Keep the last `n` successful states (history ring) |
| `WithSecretRedactor(r)` | Redact secrets in logs and snapshots (paired with `WithSecretResolver`) |

### `ReloadOption` (passed to `Manager.Reload`)

| Option | Purpose |
|---|---|
| `WithSourceOverride(map)` | Inject a one-shot override layer |
| `WithReloadReason(s)` | Override the default `"manual"` reason for audit |

---

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
profileEnv: "APP_PROFILE"     # env var to read profile (overridden by WithProfileEnv)
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
    fastconf.WithProfiles("prod", "eu-west", "canary"),
)
```

`WithProfiles` and `WithProfile` are mutually exclusive. In multi-profile
mode each overlay's `_meta.yaml.match` decides whether it applies.

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
| CLI         | `provider.NewCLIChanged(map[string]any)` | Explicitly changed CLI flag map; omit parser defaults so files/env remain authoritative unless the user typed an override |
| DotEnv      | `provider.NewDotEnv("APP_", paths...)` | Explicit `.env` fallback paths; actual process env values win even when set to `""`. Same replacer / `At` / `WithCoerce` knobs as `NewEnv` |
| Labels      | `provider.NewLabels(labels, provider.LabelOptions{})` | Low-level flat-label primitive. Default priority `PriorityStatic`; pass a higher band explicitly when the source should override |
| DottedLabels| `provider.NewDottedLabels(labels, provider.DottedLabelOptions{})` | Explicit dotted-config labels when the key path itself is the whole DSL |
| RoutingLabels| `provider.NewRoutingLabels(labels, provider.RoutingLabelOptions{})` | Routing DSL labels with typed scalars, comma lists, `[N]` indexes, and an optional enable gate. For Traefik-style inputs, opt into the matching `Prefix`, `EnableGate`, and `LowercaseKeys` settings explicitly |
| LabelMap    | `provider.NewLabelMap(labels, provider.LabelOptions{})` | `map[string]string` variant of the low-level primitive |
| K8s Downward| `k8s.NewDefault()` (`providers/k8s`) | Reads `/etc/podinfo/{labels,annotations}` as raw metadata under `k8s.metadata.*`; mounted files automatically join the shared fs watcher when `WithWatch(true)` is enabled. Use `NewExpandedDefault()` or `MetadataExpanded` only when you intentionally want config-style expansion |

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
| `providers/s3/s3events` | root module pkg | SQS long-poll (EventBridge) | — | n/a (watch-only) | static AWS creds | `no_provider_s3events` |

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
    fastconf.WithWatch(true),
    // Defaults to ProfileK8s (quiet=30ms / maxLag=250ms / swapHint=5ms).
    // Switch presets, or tweak one knob:
    fastconf.WithCoalesceQuiet(50*time.Millisecond),
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
    fastconf.WithProfileEnv("TENANT_A_PROFILE"),
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

## Sub-module ecosystem

### Shipped with the root module (same version, regular import)

| Package | Path | Notes |
|---|---|---|
| contracts | `contracts` | Public interfaces: Provider / Codec / Source / Event |
| pkg/* | `pkg/{decoder,discovery,feature,flog,generator,mappath,merger,migration,profile,provider,transform,validate}` | Reusable primitives |
| internal/* | `internal/{debounce,obs,typeinfo,watcher}` | Compile-time API boundary |
| http        | `providers/http`   | HTTP / SSE provider (build tag `no_provider_http`) |
| vault       | `providers/vault`  | HashiCorp Vault KV v2 (build tag `no_provider_vault`) |
| consul      | `providers/consul` | Consul KV (build tag `no_provider_consul`) |
| policy      | `policy`           | Policy interface + Func adapter |
| integrations/bus | `integrations/bus` | Configuration change bus |
| integrations/render | `integrations/render` | Template render extension |
| cmd/fastconfd | `cmd/fastconfd`  | Sidecar HTTP + SSE service |

### Independent sub-modules (`go get` as needed)

| Sub-module | Path | Tag prefix | Primary dependency |
|---|---|---|---|
| validate/playground | `validate/playground` | `validate/playground/vX.Y.Z` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | `observability/metrics/prometheus/vX.Y.Z` | prometheus/client_golang |
| otel | `observability/otel` | `observability/otel/vX.Y.Z` | OpenTelemetry SDK |
| cue (unified) | `cue` | `cue/vX.Y.Z` | cuelang.org/go (CUE validation + policy) |
| opa-policy | `policy/opa` | `policy/opa/vX.Y.Z` | open-policy-agent/opa |
| log/phuslu | `integrations/log/phuslu` | `integrations/log/phuslu/vX.Y.Z` | phuslu/log |
| log/zerolog | `integrations/log/zerolog` | `integrations/log/zerolog/vX.Y.Z` | rs/zerolog |
| cli/pflag | `integrations/cli/pflag` | `integrations/cli/pflag/vX.Y.Z` | spf13/pflag |
| nats provider | `providers/nats` | root-versioned (`vX.Y.Z`) | root module only (caller injects `nats.Conn`) |
| redis-streams provider | `providers/redisstream` | root-versioned (`vX.Y.Z`) | root module only (caller injects redis client) |
| openfeature | `integrations/openfeature` | root-versioned (`vX.Y.Z`) | root module only |
| s3 provider | `providers/s3` | `providers/s3/vX.Y.Z` | AWS SDK v2 (load + ETag short-circuit, `FromURL` helper) |
| s3events provider | `providers/s3/s3events` | root-versioned via `providers/s3` | AWS SDK v2 SQS (EventBridge S3 → SQS watch, subpackage of s3) |
| cmd/fastconfctl | `cmd/fastconfctl` | `cmd/fastconfctl/vX.Y.Z` | root module only |
| cmd/fastconfgen | `cmd/fastconfgen` | `cmd/fastconfgen/vX.Y.Z` | yaml.v3 |

Tag every sub-module at once via `tools/tag-release.sh`:

```bash
./tools/tag-release.sh vX.Y.Z          # local tags only
./tools/tag-release.sh vX.Y.Z --push   # push and trigger release.yml
./tools/tag-release.sh vX.Y.Z --force --push
./tools/tag-release.sh vX.Y.Z --delete --push
```

---

## Extension guide

### Custom Provider

```go
type RedisProvider struct {
    client *redis.Client
    key    string
    ch     chan contracts.Event
}

func (p *RedisProvider) Name()     string { return "redis:" + p.key }
func (p *RedisProvider) Priority() int    { return contracts.PriorityKV }

func (p *RedisProvider) Load(ctx context.Context) (map[string]any, error) {
    raw, err := p.client.Get(ctx, p.key).Bytes()
    if err != nil { return nil, err }
    var out map[string]any
    return out, json.Unmarshal(raw, &out)
}

func (p *RedisProvider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
    go p.watchLoop(ctx)
    return p.ch, nil
}

func init() {
    fastconf.RegisterProviderFactory("redis", func(cfg map[string]any) (contracts.Provider, error) {
        return NewRedisProvider(cfg["addr"].(string), cfg["key"].(string))
    })
}
```

### Custom Transformer

```go
type PrefixTransformer struct{ Prefix string }

func (t PrefixTransformer) Name() string { return "prefix:" + t.Prefix }
func (t PrefixTransformer) Transform(root map[string]any) error {
    if v, ok := root["app_name"].(string); ok {
        root["app_name"] = t.Prefix + "-" + v
    }
    return nil
}

fastconf.WithTransformers(PrefixTransformer{Prefix: "myorg"})
```

### Custom Codec

YAML, JSON, and TOML are registered automatically. Register a new
format like this:

```go
fastconf.RegisterCodec("hcl", hclCodec{})
fastconf.RegisterCodecExt("hcl", "hcl") // .hcl files route to "hcl"
```

### Picking an extension point

| Need | Use |
|---|---|
| Add a data source | implement `contracts.Provider` |
| Rewrite the merged tree | implement `Transformer` |
| Decrypt leaves before decode | implement `SecretResolver` |
| Type-rewrite leaves before decode | implement `decoder.TypedHook` |
| Assert after decode | `WithValidator` / `WithPolicy` |
| Act on successful publish | `AuditSink` / `DiffReporter` |
| Add a file format | implement `contracts.Codec` + `RegisterCodec` |

---

## CLI tools

### `fastconfd` — sidecar service

```bash
fastconfd --dir=/etc/config --profile=prod --addr=:8081
```

| Endpoint | Method | Description |
|---|---|---|
| `/healthz` | GET  | `{"status":"ok","generation":N}` |
| `/version` | GET  | Current state version (Hash + Generation) |
| `/config`  | GET  | Current config JSON (secrets redacted) |
| `/reload`  | POST | Trigger a manual reload; accepts `{"request_id":"…"}` |
| `/events`  | GET  | SSE stream of `ReloadCause` JSON on every successful reload |

### `fastconfctl` — admin CLI

```bash
fastconfctl snapshot --addr=:8081
fastconfctl reload   --addr=:8081 --request-id=deploy-123
fastconfctl plan     --addr=:8081
fastconfctl rollback --addr=:8081 --generation=42
fastconfctl sources  --addr=:8081
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
| `BenchmarkIntrospectCold` | 1.67 µs/op |
| `BenchmarkExplainDeep` | 219 ns/op |

Full baseline, command lines, and explanation: [`docs/design/perf.md`](docs/design/perf.md).

The contract is: **hot reads are essentially free; reload may fail but
never publishes a half-built state; subscriber fan-out never blocks the
read path.**

---

## Development

```bash
# Dependencies
go mod tidy

# Build / test / lint
make build
make test         # go test -race -count=1 ./...
make test-all     # includes sub-modules
make lint         # requires golangci-lint

# Examples
go test ./... -run '^Example' -v

# Benchmarks
go test -bench=BenchmarkGet -benchmem ./...

# CI guards
bash tools/check-layout.sh
bash tools/check-doc-symbols.sh
bash tools/check-deps.sh
bash tools/bench-guard.sh
bash tools/loc-budget.sh
bash tools/total-loc-budget.sh

# Code-review dependency graph
bash tools/code-review-graph.sh
```

---

## Documentation map

| Doc | Purpose |
|---|---|
| [`docs/cookbook/README.md`](docs/cookbook/README.md) | Single entry point for every recipe |
| [`docs/design/spec.md`](docs/design/spec.md) | Runtime model, concurrency, module boundaries |
| [`docs/design/perf.md`](docs/design/perf.md) | Latest benchmark baseline |
| [`CHANGELOG.md`](CHANGELOG.md) | Release notes |
| [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc and runnable examples |

Common recipes:

- [`k8s`](docs/cookbook/k8s.md) · [`reload-policy`](docs/cookbook/reload-policy.md) · [`plan`](docs/cookbook/plan.md)
- [`vault`](docs/cookbook/vault.md) · [`consul`](docs/cookbook/consul.md) · [`cross-process`](docs/cookbook/cross-process.md) · [`provider-timeouts`](docs/cookbook/provider-timeouts.md)
- [`secrets`](docs/cookbook/secrets.md) · [`features`](docs/cookbook/features.md) · [`openfeature`](docs/cookbook/openfeature.md)
- [`diff-reporter`](docs/cookbook/diff-reporter.md) · [`policy`](docs/cookbook/policy.md) · [`otel`](docs/cookbook/otel.md)
- [`introspect`](docs/cookbook/introspect.md) · [`field-meta`](docs/cookbook/field-meta.md) · [`typed-hooks`](docs/cookbook/typed-hooks.md)
- [`labels`](docs/cookbook/labels.md) · [`strategic-merge`](docs/cookbook/strategic-merge.md) · [`generators`](docs/cookbook/generators.md)
- [`tenant`](docs/cookbook/tenant.md) · [`sidecar`](docs/cookbook/sidecar.md) · [`dump`](docs/cookbook/dump.md) · [`log`](docs/cookbook/log.md)

---

## License

MIT License, See [`LICENSE`](LICENSE).

Copyright (c) 2026 FastAbc
