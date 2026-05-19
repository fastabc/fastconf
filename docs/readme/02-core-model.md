# 02 — Core Model

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
manager.go           — Manager[T] wrapper, MustNew, Subscribe, TenantManager facade
state.go             — State[T], DiffEntry/DiffChange, Dump, SourcePriorityBand
options.go           — WithXxx option builders + ProfileOptions / WatchOptions / CoalesceOptions
aliases.go           — codec, secret, field-meta public facades
errors.go            — public sentinel errors and ReloadError stream
obs.go               — metrics, tracer, audit-sink facades
defaults.go          — WithStructDefaults + Defaulter interface + WithDefaults
feature.go           — FeatureRule extraction + Eval[T,V]
presets.go           — PresetK8s, PresetSidecar, PresetTesting, PresetHierarchical
registry.go          — RegisterProviderFactory + WithProviderByName
bind.go              — WithSource content-type helpers
doc.go               — package-level godoc
internal/manager     — Manager[T] body, pipeline, reload loop, watchers
internal/state       — State[T], history ring, provenance, diff
internal/options     — Options struct and deferred provider resolution
internal/secret      — secret-tag scanner and resolver
internal/pipeline    — struct defaults + field-meta runners
internal/obs         — metrics/tracer/audit bridge types
internal/provenance  — Origin + OriginIndex
```

### Dependency direction (CI-enforced)

```
fastconf  →  internal/{manager,options,state,tenant,obs}
          →  pkg/{discovery,decoder,flog,merger,provider,validate}
          →  contracts

pkg/* MUST NOT depend on each other except via this whitelist
(kept in sync with tools/check-deps.sh):
  pkg/discovery → pkg/profile
  pkg/generator → pkg/mappath
  pkg/provider  → pkg/decoder
  pkg/provider  → pkg/mappath
  pkg/transform → pkg/mappath
internal/* dependencies are implementation details; public code imports the
root package facade.
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
func (s *State[T]) Dump(format DumpFormat, redactor SecretRedactor) ([]byte, error) // DumpYAML/DumpJSON/DumpTOML
func (s *State[T]) Diff(other *State[T]) []DiffEntry         // structured per-path diff; use FormatDiff for line output
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

The file-watcher is configured through one bundled Option:

| Option | Purpose | Default |
|---|---|---|
| `WithWatch(WatchOptions{...})` | Bundle for `Enabled`, `Paths`, `Coalesce`, `CoalesceProfile` | `Enabled:false` |
| `WithCoalesce(CoalesceOptions{...})` | Fine-tune `Quiet` / `MaxLag` / `SwapHint` independently of `WithWatch` | — |

`WatchOptions` and `CoalesceOptions` are plain structs — zero-value
fields keep the framework defaults. `CoalesceProfile` is a preset
(`ProfileK8s` default, or `ProfileLocalDev`) and per-field `Coalesce`
values override the profile.

The watcher debounces fsnotify events per **parent directory** rather than
globally, so independent ConfigMaps (or watched dirs) never block each
other. When a K8s atomic-swap commit (`..data` rename/create) is observed,
the coalescer tightens the window to `swapHint` (5ms) instead of waiting
the full `quiet` window — typical reload latency drops from ~500ms (the
prior global debouncer default) to ~5–35ms.

### Profile

All profile knobs are bundled into one Option:

| `ProfileOptions` field | Meaning |
|---|---|
| `Single string` | Explicit single profile |
| `Multi  []string` | Multi-profile mode (overlays match via `_meta.yaml.match`) |
| `Expr   string` | Global profile-matching expression (AND-ed per overlay) |
| `EnvVar string` | Env var to read when `Single` / `Multi` are empty |
| `Default string` | Fallback when the env var is unset |

```go
fastconf.WithProfile(fastconf.ProfileOptions{EnvVar: "APP_PROFILE", Default: "dev"})
fastconf.WithProfile(fastconf.ProfileOptions{Multi: []string{"prod", "eu"}})
```

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
    fastconf.WithProvider(provider.NewCLI(cliMap)),                       // explicit CLI overrides only
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
| `WithDefaults[T](fn)` | Custom defaulter for `*T` (function form of `Defaulter` interface) |
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

