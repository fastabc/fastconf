# Migration guide: v0 → v1

v1 narrows FastConf's public surface to one way of doing each thing. The
kernel is unchanged: one reload goroutine, lock-free `Get()`, failed reloads
keep the previous snapshot. Every removed entry point has a direct
replacement below. Sections are added in the order the changes landed.

Behavior fixes that shipped before v1 (layer priority ordering, `Load`
honoring `WithDir`, `source.*` default priorities, provider watches without
`WithWatch`, `New` context scope) are listed in the CHANGELOG and are not
repeated here.

## Summary

| v0 | v1 |
|---|---|
| `NewTenantManager[T]()` + `tm.Add(ctx, id, opts...)` | `New[T](ctx, append(opts, WithTenant(id))...)` kept in your own map |
| `ErrTenantExists`, `ErrUnknownTenant` | your registry's own errors |
| `WithFeatureRules(extract)` + `Eval(mgr, key, ctx, def)` | `feature.Eval(mgr.Get().Features, key, ctx, def)` |
| `State.FeatureRules()` | the rules field on `State.Value()` |
| `integrations/openfeature` | Removed; evaluate `feature.Eval` directly or integrate the real SDK in your application |
| `MustNew(ctx, opts...)` | `New` + `if err != nil { panic(err) }` |
| `Extract(state, pick)` | `pick(state.Value())` |
| `EvalContext`, `FeatureRule` aliases | `feature.EvalContext`, `feature.Rule` |
| `integrations/bus` | reference implementations in `examples/nats`, `examples/redisstream` |
| `WithSource(src, parser)` / `Bind(src, parser)` | `WithProvider(src)` — `source.*` are providers, decoded by content-type |
| `Provider.Priority() int` | optional `Describe() contracts.ProviderInfo{Priority, WatchPaths}` |
| `Provider.Load(ctx) (map[string]any, error)` + `SnapshotProvider.LoadSnapshot` | `Load(ctx) (contracts.Snapshot, error)` |
| `Provider.Watch(ctx)` + `Resumable.WatchFrom(ctx, rev)` | `Watch(ctx, from string)` |
| `contracts.ErrResumeUnsupported` | subscribe cold and set `Event.Gap` |
| `WatchPathProvider.WatchPaths()` | `ProviderInfo.WatchPaths` |
| `contracts.Source`, `contracts.Parser`, `codec.*Parser`, `codec.RegisterParser/LookupParser` | `codec.ByContentType`, `codec.Register` + `codec.RegisterExt` |
| `NewValidator[T](schema)` + `contracts.Schema` | `cuelang.Validate[T](schema)` in the `cue` module |
| `contracts.Band*`, `PriorityOverlay`, `PriorityGenerator`, `PriorityOrderedBase` | internal; use `WithProfile` / `WithAxes` to order file layers |
| `contracts.Span`, `contracts.Attr` | `fastconf.Span` |
| `ErrParserUnknown` | `codec.ErrUnknownCodec` |
| `mgr.Plan().WithHostname(h).Run(ctx)` / `PlanBuilder` | `mgr.Plan(ctx, WithPlanHostname(h))` |
| `mgr.Replay().List()` / `.Rollback(s)` / `Replay` | `mgr.History().List()` / `.Rollback(s)` / `History` |
| `mgr.Watcher().Pause()` / `Resume()` / `Paused()` / `Watcher` | `mgr.Pause()` / `Resume()` / `Paused()` |
| `WithProfile(ProfileOptions{Single: "p"})` | `WithProfile(Profile{Names: []string{"p"}})` |
| `ProfileOptions{Multi, Expr, EnvVar, Default}` | `Profile{Names, Match, Env, Default}` |
| `WithMultiAxisOverlays(OverlayAxis{Dir, EnvVar, Priority: 3200, DefaultFromHostname})` | `WithAxes(Axis{Dir, Env, Priority: 200, FromHostname})` — priority is relative among axes |
| `WithProviderOrdered(a, b, c)` | `WithProvider(a, b, c)` — equal priorities merge in declaration order |
| `WithProviderByName`, `WithProviderRegistry`, `RegisterProviderFactory`, `LookupProviderFactory`, `RegisteredProviderNames`, `NewProviderRegistry`, `ProviderFactory`, `ProviderRegistry` | construct the provider and pass it to `WithProvider`; the CLI keeps its own `cli.Providers` map |
| `WithDotEnvAuto(prefix)` | `WithProvider(dotenv.NewDotEnv(prefix, dotenv.AutoDotEnvPaths(dir)...))` |
| `PresetK8s`, `PresetSidecar`, `PresetTesting`, `PresetHierarchical` + `*Opts`, `DefaultSidecarHistoryCap` | the explicit options they expanded to (see below) |
| `WithTransformers(t1, t2)` / `fastconf.Transformer` | `WithTransform(t1, t2)` — plain `func(map[string]any) error` |
| `WithMigrations(fn)` / `MigrationApplier` / `MigrationFunc` | `WithTransform(fn)` placed first |
| `WithRawMapAccess(fn)` | a read-only `WithTransform` placed last |
| `WithValidator[T](fn)` | `WithValidate[T](fns...)` |
| `WithStrict(b)` | `WithStrictMerge(b)` |
| `WithCodecBridge(BridgeYAML)` / `CodecBridge` / `BridgeJSON` | `WithDecoder(YAML)` / `Format` / `JSON` |
| `WithStructDefaults[T]()` | remove — `fc:"default=…"` always applies |
| `WithDefaults[T](fn)` | implement `Defaulter` on `*T`, or set values in a `WithValidate` function |
| `DumpFormat`, `DumpYAML`, `DumpJSON`, `DumpTOML` | `Format`, `YAML`, `JSON`, `TOML` |
| `WithWatch(WatchOptions{Enabled: true, Coalesce: CoalesceOptions{Quiet: q}, CoalesceProfile: p})` | `WithWatch(Watch{Quiet: q, Profile: p})` — calling WithWatch enables it |
| `WithCoalesce(CoalesceOptions{...})` | the same fields on `Watch` |
| `WithSecretRedactor(r)` | `WithRedactor(r)` |
| `WithSourceOverride(m)` / `WithReloadReason(s)` | `WithOverride(m)` / `WithReason(s)` |
| `RegisterCodec`, `RegisterCodecExt`, `LookupCodec` | `codec.Register`, `codec.RegisterExt`, `codec.Lookup` |
| `ParseFieldTag`, `FieldMetaFor`, `FieldSpec` | removed (internal reflection plan) |
| `state.Redacted()`, `state.Redact(r)` | `state.Map()` (redactor from `WithRedactor`) |
| `state.Dump(f, nil)` | `state.Unredacted().Dump(f)` — plaintext is now explicit |
| `state.Dump(f, fastconf.DefaultSecretRedactor)` | `state.Dump(f)` |
| `state.Diff(other)` (plaintext values) | `state.Diff(other)` now shows masked values; changes are still detected on plaintext |
| `state.Introspect().Settings()` / `.Keys()` / `.At(p)` | `state.Map()` + `confmap.GetDotted` |
| `state.Origins()`, `state.LookupStrict(p)`, `OriginIndex` | `state.Explain(p)` (values masked for secrets) |
| `state.LoadedAt()` | `state.Cause().At` |
| fastconfd `/config?redact=true` | `/config` (masked by default); plaintext via `?unredacted=true` + `X-Unredacted-Token` |
| `WithMetrics(sink)` + `MetricsSink` family | `WithObserver(observe.Metrics(sink))` |
| `WithAuditSink(fastconf.NewJSONAuditSink(w))` | `WithObserver(observe.JSONLines(w))` |
| `WithAuditSink(AuditSinkFunc(fn))` | `WithObserver(observe.Func(...))` handling `Committed` (`c.Cause`) |
| `WithDiffReporter(r)` + `WithDiffReporterQueueCap(n)` | `WithObserver(observe.Async(o, n))` handling `Committed` (`c.Diff()`) |
| `DiffEvent.Reason` / `.PrevGeneration` / `.NewGeneration` / `.At` / `.Diff` / `.Cause` | `Committed.Cause.Reason` / `.Prev` / `.Next` / `.Cause.At` / `.Diff()` / `.Cause` |
| `WithAuditTimeout`, `WithDiffReporterTimeout` | `WithObserverTimeout` |
| `ErrValidator`, `ErrPolicyDenied`, `ErrValidation` | `ErrInvalid` |
| `ErrPatch` | `ErrMerge` |
| `ErrGenerator` | `ErrProvider` |
| `ErrConfigTooLarge` (root) | `ErrTooLarge` (`contracts.ErrConfigTooLarge` is the same value) |
| `ErrNoOrigin` | removed (never returned) |
| `labels.NewLabels` / `NewLabelMap` / `NewDottedLabels` / `NewDottedLabelMap` + `LabelOptions` | `labels.New(input, labels.Options{...})` |
| `labels.NewRoutingLabels` / `NewRoutingLabelMap` + `RoutingLabelOptions{EnableGate, ...}` | `labels.New(input, labels.Options{Prefix: ..., Routing: &labels.Routing{EnableGate: ...}})` |
| `LabelOptions.Separator: "/"` | `Options.Separators: []string{"/"}` |
| `transform.ExpandLabels(at, to, opts)` | a label provider, or a transform calling `confmap.ExpandLabels` ([labels.md](labels.md) §3) |
| `transform.MergeByKey(path, key)` | `WithMergeKeys(map[string]string{path: key})`; merge list items across layers before transforms run |
| `transform.X(...).Transform` | `transform.X(...)` — built-ins are plain functions |
| `transform.Defaults(m)`, `transform.SetIfAbsent(p, v)` | `fc:"default=…"` tags or a `Defaulter` on `*T` |
| `transform.Wrap`, `transform.ErrTransform`, `transform.Transformer` | removed; the transform stage wraps failures in `fastconf.ErrTransform` |
| `SourcePriorityBand(ref)` / `ref.Band()` | removed; use the source `Kind`, `Priority`, `Profile` and `Path` fields |
| `WithRedactor(fastconf.DefaultSecretRedactor)` | omit it — that mask is the default |
| `overlay.Scan`, `overlay.ScanOptions`, `overlay.Compile`, `overlay.LoadMeta`, … | internal; use `WithDir` / `WithFS` / `WithProfile` / `WithAxes` |

`PolicyError` is defined directly in `fastconf` instead of aliasing an internal
error type. Its `Violations` field, message format and `errors.Is` matching for
`ErrInvalid` and `ErrFastConf` are unchanged. `WithHistory(n)` now rejects negative
capacities; use zero to disable history.

`confmap.NormalizeLabelInput` and `confmap.LabelPair` remain shared APIs because
both `confmap.ExpandLabels` and the label provider use them.

## Tenants

`TenantManager` was a `map[string]*Manager` behind a lock that could neither
load lazily nor evict. Keep managers in your own registry and tag each with
`WithTenant(id)`; the id still reaches `ReloadCause.Tenant` and
`policy.Input.Tenant`. [tenant.md](tenant.md) has a reference registry.

## Feature flags

Feature rules are an ordinary field of `T`. Evaluate them from the live
snapshot instead of registering an extractor:

```go
// v0
fastconf.WithFeatureRules(func(c *Cfg) map[string]feature.Rule { return c.Features })
on := fastconf.Eval(mgr, "dark", evalCtx, false)

// v1
on, _ := feature.Eval(mgr.Get().Features, "dark", evalCtx, false).(bool)
```

## Module boundaries

`integrations/bus` is removed: nothing in the repository used its in-memory
broker, and the NATS / Redis Streams providers never built on it. Use those
reference implementations in examples for cross-process push.

`cmd/fastconfgen` is now its own module (`cmd/fastconfgen/vX.Y.Z` tags). The
install command is unchanged:

```bash
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

## Provider contract

`contracts.Provider` is one interface; capabilities are values, not extra
interfaces discovered by type assertion:

```go
type Provider interface {
    Name() string
    Load(ctx context.Context) (Snapshot, error)
    Watch(ctx context.Context, from string) (<-chan Event, error)
}
type Describer interface{ Describe() ProviderInfo } // optional
```

Porting a v0 provider:

```go
// v0
func (p *P) Priority() int { return contracts.PriorityKV }
func (p *P) Load(ctx context.Context) (map[string]any, error) { return p.m, nil }
func (p *P) Watch(ctx context.Context) (<-chan contracts.Event, error) { return p.ch, nil }

// v1
func (p *P) Describe() contracts.ProviderInfo { return contracts.ProviderInfo{Priority: contracts.PriorityKV} }
func (p *P) Load(ctx context.Context) (contracts.Snapshot, error) { return contracts.Snapshot{Map: p.m}, nil }
func (p *P) Watch(ctx context.Context, from string) (<-chan contracts.Event, error) { return p.ch, nil }
```

`from` is the last revision FastConf observed ("" on the first subscribe). A
provider that cannot resume ignores it and sets `Event.Gap` on its first
event. File-backed providers report their paths in `ProviderInfo.WatchPaths`
instead of implementing `WatchPathProvider`.

Byte documents no longer need a parser: `source.NewFile/NewBytes`
are providers and choose the codec from the content-type hint.

```go
// v0
fastconf.WithSource(source.NewFile("/etc/app.yaml"), codec.YAMLParser())
// v1
fastconf.WithProvider(source.NewFile("/etc/app.yaml"))
```

## Manager API

`Manager[T]` and `State[T]` are defined in the root package; the wrapper
types around them are gone.

```go
// v0
res, err := mgr.Plan().WithHostname("prod-eu-1").Run(ctx)
states := mgr.Replay().List()
err = mgr.Replay().Rollback(states[0])
mgr.Watcher().Pause()

// v1
res, err := mgr.Plan(ctx, fastconf.WithPlanHostname("prod-eu-1"))
h := mgr.History() // nil without WithHistory
states := h.List()
err = h.Rollback(states[0])
mgr.Pause()
```

## Presets

The presets were a handful of options each; spell them out:

```go
// PresetK8s(K8sOpts{Dir: "/etc/config", Watch: true})
fastconf.WithDir("/etc/config"),
fastconf.WithProfile(fastconf.Profile{Env: "APP_PROFILE", Default: "default"}),
fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
fastconf.WithStrict(true),

// PresetSidecar(SidecarOpts{Dir: d, HistoryN: 16, Watch: true})
fastconf.WithDir(d), fastconf.WithHistory(16), fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),

// PresetTesting(TestingOpts{FS: fsys, Profile: "testing"})
fastconf.WithFS(fsys), fastconf.WithProfile(fastconf.Profile{Names: []string{"testing"}}),
```

`PresetHierarchical` used axis priorities 3000/3100/3200; with `WithAxes` the
same order is `Priority: 0/100/200` (or `0/1/2`).

## Axis precedence

Higher `Axis.Priority` wins across whole directories, regardless of how many
files a lower-priority axis contains. Within each axis, filenames determine
order. Previous versions could let additional files in a lower-priority axis
win accidentally. Use `fastconfctl explain` to check the winning source for
fields defined by more than one axis.

## Pipeline options

Struct-tag defaults are always on. If a field silently gained a value after
upgrading, it carries an `fc:"default=…"` tag that `WithStructDefaults` used
to gate.

The migration stage is folded into transforms, so the tracer no longer
emits a `fastconf.migration` span; put the migration first in
`WithTransform` to keep its position:

```go
// v0
fastconf.WithMigrations(chainRun),
fastconf.WithTransformers(transform.EnvSubst(), transform.Aliases(m)),
// v1
fastconf.WithTransform(chainRun, transform.EnvSubst(), transform.Aliases(m)),
```

## Snapshot views

Every map or text view of a `State` masks secrets by default: `Map`, `Dump`,
`Diff` and `Explain`. Code that genuinely needs plaintext says so:

```go
// v0
b, _ := st.Dump(fastconf.DumpYAML, nil)   // plaintext
m := st.Redacted()                          // masked
// v1
b, _ := st.Unredacted().Dump(fastconf.YAML) // plaintext, greppable
m := st.Map()                               // masked
```

fastconfd follows suit: `/config` and `/dump` are masked unless the daemon
runs with `-unredacted-token` and the request sends `?unredacted=true` with a
matching `X-Unredacted-Token`.

## Unknown keys

Keys that no field of `*T` decodes used to vanish silently. v1 logs the first
one (`UnknownWarn`, the default). Fail instead with
`WithUnknownFields(fastconf.UnknownError)`, or restore the old silence with
`WithUnknownFields(fastconf.UnknownIgnore)`.

## Observers

Metrics sinks, audit sinks and diff reporters were three hooks with three
delivery models. v1 has one `Observer` receiving typed events; the `observe`
package rebuilds the old behaviors:

```go
// v0
fastconf.WithMetrics(promSink),
fastconf.WithAuditSink(fastconf.NewJSONAuditSink(os.Stderr)),
fastconf.WithDiffReporter(fastconf.DiffReporterFunc(func(ctx context.Context, ev fastconf.DiffEvent) error {
    return slack.Post(ctx, ev.Diff)
})),
// v1
fastconf.WithObserver(observe.Multi(
    observe.Metrics(promSink),
    observe.JSONLines(os.Stderr),
    observe.Async(observe.Func(func(ctx context.Context, e fastconf.Event) {
        if c, ok := e.(fastconf.Committed); ok && c.Prev != 0 {
            _ = slack.Post(ctx, c.Diff())
        }
    }), 64),
)),
```

Two differences to check: observers run on the reload goroutine (wrap
anything slow in `observe.Async`), and `Committed` also fires for the
initial load (`Prev == 0`), which diff reporters used to skip.

## v1.0.0 module consolidation

Logging adapters keep their import paths and remain separate modules
(`integrations/log/phuslu`, `integrations/log/zerolog`). Their stdlib-only shared
implementation now lives in the root module; no `integrations/log` module is
required. Depending on one adapter does not pull in the other backend.
Prometheus and OTel retain the original leaf modules
`observability/metrics/prometheus` and `observability/otel`. If you used the
unreleased combined `observability` module, drop that requirement and require
only the leaf modules you use, then run `go mod tidy`. Do not require both the
combined parent and leaf modules: they contain the same import paths.
All eleven modules release together; manifests pin the same root candidate.
The v1.0.0 tags have not been created yet.

`dotenv.EnvKeyReplacer`, `dotenv.DotReplacer`, and
`dotenv.DoubleUnderscoreReplacer` are removed. Use their `providers/env`
equivalents with `WithReplacer`; copied mutable variables no longer disagree.
The `no_provider_*` build tags are removed. Go only links imported packages.

`fc:"required,min=,max=,oneof=,default="` applies to struct fields and nested
struct/pointer fields, not fields inside slice, array, or map elements. Use a
`Defaulter` for element defaults and `WithValidate` for element constraints.

NATS and Redis Streams moved to `examples/nats` and `examples/redisstream` as
reference implementations to copy and adapt. The OpenFeature-shaped adapter was
removed; it did not implement the OpenFeature SDK contract. The sidecar endpoints
(including `/dump` and `/version`) remain available.

`source.HTTPSource` / `source.NewHTTP` are removed. Use the HTTP provider:

```go
import httpprov "github.com/fastabc/fastconf/providers/http"
p, err := httpprov.New("remote", url, nil, httpprov.WithInterval(0))
// Check err, then pass p to fastconf.WithProvider.
```

A nil codec selects by response Content-Type. An explicit codec still overrides
that header. `WithInterval(0)` preserves manual-only fetching; omit it for 30s
polling. Replace chained setters with constructor options `WithClient`,
`WithPriority`, and `WithMaxBodyBytes`. Use `Load` for a decoded snapshot;
raw HTTP `Read` is no longer exposed. The default client rejects redirects,
and a 304 without a successfully decoded snapshot is an error.
