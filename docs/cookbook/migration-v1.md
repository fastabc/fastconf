# Migration: v0.20.0 → v1.1.0

The tables document the API migration from v0.20.0 to v1.1.0. These changes
also apply when upgrading directly to v1.2.0; intermediate development APIs
are not migration steps. Install the current root and optional modules together:

```sh
go get github.com/fastabc/fastconf@v1.2.0
# Example optional module; update the adapters your application imports.
go get github.com/fastabc/fastconf/providers/s3@v1.2.0
go mod tidy
```

The root now requires Go 1.24 rather than Go 1.22. Optional modules can require
a newer toolchain; check their `go.mod` files. Import paths remain under
`github.com/fastabc/fastconf`.

## Constructors and options

| v0.20.0 | v1.1.0 |
|---|---|
| `env.NewEnv(prefix)` / `env.NewEnvReplacer(prefix, r)` | `env.New(prefix)` / `env.New(prefix).WithReplacer(r)` |
| `dotenv.NewDotEnv(prefix, paths...)` | `dotenv.New(prefix, paths...)` |
| `cliflag.NewCLI(values)` | `cliflag.New(values)` |
| `env.EnvProvider`, `dotenv.DotEnvProvider`, `cliflag.CLIProvider` | `Provider` in each respective package |
| `labels.NewLabels`, `NewLabelMap`, `NewDottedLabels`, `NewDottedLabelMap` | `labels.New(input, labels.Options{...})` |
| `labels.LabelProvider`, `LabelOptions`, `DottedLabelOptions` | `labels.Provider`, `labels.Options` |
| `labels.NewRoutingLabels`, `NewRoutingLabelMap`, `RoutingLabelOptions` | `labels.New(input, labels.Options{Routing: &labels.Routing{...}})` |
| `LabelOptions.Separator` | `Options.Separators` (a slice) |
| `source.FileSource`, `source.BytesSource` | `source.File`, `source.Bytes`; keep `NewFile`, `NewBytes` |
| `ProfileOptions{Single, Multi, Expr, EnvVar, Default}` | `Profile{Names, Match, Env, Default}`; `Single: "prod"` becomes `Names: []string{"prod"}` |
| `WatchOptions{Enabled: true, Coalesce: ..., CoalesceProfile: ...}` | `Watch{Quiet, MaxLag, SwapHint, Profile, Paths}`; calling `WithWatch` enables watching |
| `WithCoalesce(CoalesceOptions{...})` | Put the timing fields in `WithWatch(Watch{...})` |
| `WithMultiAxisOverlays(OverlayAxis{Dir, EnvVar, Priority, DefaultFromHostname})` | `WithAxes(Axis{Dir, Env, Priority, FromHostname})` |
| `WithStrict(b)` | `WithStrictMerge(b)` |
| `WithCodecBridge(BridgeJSON/BridgeYAML)` | `WithDecodeFormat(JSON/YAML)` |
| `WithValidator[T](fn)` | `WithValidate[T](fn)` |
| `WithStructDefaults[T]()` | Remove; `fc:"default=…"` tags always apply |
| `WithDefaults[T](fn)` | Implement `Defaults()` on `*T` (`Defaulter`) |
| `WithSecretRedactor(r)` | `WithRedactor(r)` |
| `WithSourceOverride(m)`, `WithReloadReason(s)` | `WithOverride(m)`, `WithReason(s)` |
| `WithProviderOrdered(a, b, c)` | `WithProvider(a, b, c)`; equal priorities keep registration order |
| `WithDotEnvAuto(prefix)` | `WithProvider(dotenv.New(prefix, dotenv.AutoDotEnvPaths(dir)...))` |
| `PresetK8s`, `PresetSidecar`, `PresetTesting`, `PresetHierarchical` | Register explicit directory, profile, watcher, history, FS and axis options |
| Provider registry/factory APIs and `WithProviderByName` | Construct providers in the application and pass them to `WithProvider` |

`WithProvider`, `WithGenerator`, `WithTypedHook`, `WithValidate` and `WithPolicy`
accept multiple values. Axis priorities are relative to other axes: replace
3000/3100/3200 with 0/100/200. Providers always override directory and generator
layers; a numeric priority only orders providers against each other.

For a former Kubernetes preset:

```go
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithDir("/etc/config"),
    fastconf.WithProfile(fastconf.Profile{Env: "APP_PROFILE", Default: "default"}),
    fastconf.WithWatch(fastconf.Watch{}),
    fastconf.WithStrictMerge(true),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Omit `WithWatch` when the old `WatchOptions.Enabled` was false. A zero
`Watch{}` enables watching; it does not disable it.

## Providers and document decoding

| v0.20.0 | v1.1.0 |
|---|---|
| `WithSource(src, parser)` / `Bind(src, parser)` | `WithProvider(src)`; file/byte sources decode their own documents |
| `contracts.Codec`, `codec.Decoder` | `contracts.Decoder` |
| `contracts.Parser`, parser constructors and parser registry | `codec.ByContentType`, `codec.Register`, `codec.RegisterExt` |
| Root `RegisterCodec`, `RegisterCodecExt`, `LookupCodec` | `codec.Register`, `codec.RegisterExt`, `codec.Lookup` |
| `Provider.Priority()` / `WatchPathProvider.WatchPaths()` | Optional `Describe() contracts.ProviderInfo{Priority, WatchPaths}` |
| `Load(ctx) (map[string]any, error)` / `SnapshotProvider.LoadSnapshot` | `Load(ctx) (contracts.Snapshot, error)` |
| Required `Watch(ctx)` / optional `Resumable.WatchFrom(ctx, rev)` | Optional `contracts.Watcher.Watch(ctx, from)` |
| `ErrResumeUnsupported` | Subscribe cold and set `Event.Gap` on the first event when a non-empty revision cannot be resumed |
| `source.*.Read(ctx)` | `Load(ctx)` returns decoded data; read raw bytes through application I/O |
| `source.NewHTTP(url)` | `httpprov.New(name, url, nil, httpprov.WithInterval(0))` from `providers/http` |

A static provider needs only Name and Load:

```go
func (p *Provider) Name() string { return "application" }
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
    data, err := p.loadMap(ctx) // application-owned loader
    return contracts.Snapshot{Map: data}, err
}
func (p *Provider) Describe() contracts.ProviderInfo {
    return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}
```

Remove empty Watch methods. Native watchers receive the last observed revision;
file-backed providers can supply WatchPaths without implementing Watcher.
Provider watchers run independently of `WithWatch`, which controls file watching.
Document codecs and struct decoding are separate: `WithDecodeFormat` selects
the JSON/YAML bridge into `T`; diagnostic field names always follow JSON tags.

HTTP's nil decoder selects from Content-Type. `WithInterval(0)` retains
manual-only fetching; omit it for 30-second polling. Replace HTTP source setters
with constructor options `WithClient`, `WithPriority` and `WithMaxBodyBytes`.
Root file/byte and HTTP/KV provider readers default to a 4 MiB limit and reject
oversized input with `ErrTooLarge`; S3 defaults to 10 MiB. Directory discovery
is a separate reader path. Adjust provider limits for larger documents.
Multi-document YAML is rejected rather than silently truncated.

## Manager and snapshot APIs

| v0.20.0 | v1.1.0 |
|---|---|
| `MustNew(ctx, opts...)` | `New` and explicit error handling |
| `mgr.Plan().WithHostname(h).Run(ctx)` | `mgr.Plan(ctx, WithPlanHostname(h))` |
| `mgr.Replay().List()` / `.Rollback(s)` | `mgr.History().List()` / `.Rollback(s)`; History is nil when disabled |
| `mgr.Watcher().Pause()` / `.Resume()` / `.Paused()` | `mgr.Pause()` / `.Resume()` / `.Paused()` |
| `Extract(state, pick)` | `pick(state.Value())` |
| `state.Introspect().Settings()` / `.Keys()` / `.At(p)` | `state.Map()` and `confmap.GetDotted` |
| `state.Redacted()` / `.Redact(r)` | `state.Map()`; configure the redactor with `WithRedactor` |
| `state.Origins()` / `.LookupStrict(p)` | `state.Explain(p)` with `WithProvenance` |
| `state.Dump(f, nil)` | `state.Unredacted().Dump(f)` for plaintext |
| `state.Dump(f, redactor)` | `state.Dump(f)` for the configured masked view |
| `DumpFormat`, `DumpYAML`, `DumpJSON`, `DumpTOML` | `Format`, `YAML`, `JSON`, `TOML` |
| `state.LoadedAt()` | `state.Cause().At` (`time.Time`) |
| `ReloadCause.At` Unix nanoseconds / `ReloadError.When` | `At time.Time`; use `At.UnixNano()` only for integer consumers |
| `ErrValidation`, `ErrValidator`, `ErrPolicyDenied` | `ErrInvalid`; use `errors.As` for `*fastconf.PolicyError` findings |
| `ErrPatch`, `ErrGenerator` | `ErrMerge`, `ErrProvider` respectively |
| `ErrNoOrigin`, exported reflection/overlay helpers and priority bands | Removed; use `Explain`, options and source `Kind`, `Priority`, `Profile`, `Path` |

`Get()` and every reachable map or slice remain read-only. Constructor context
cancellation only bounds initialization; stop workers with `Close` or
`Shutdown`. Negative `WithHistory` capacities are errors; zero disables history.
Pause suppresses watcher triggers; Resume does not replay them. Reload explicitly
after a paused batch. `Plan` collects validator/policy findings without publishing;
a nil error is not an acceptance decision. See [Plan](runtime/plan.md).

`Map`, `Dump`, `Diff` and `Explain` now mask tagged/path-matched secrets by default.
Unknown struct keys warn by default; choose `WithUnknownFields(UnknownError)` to
reject them or `UnknownIgnore` to keep the v0.20.0 silent-drop behavior. Defaults
and constraints on collection element fields require `Defaulter` / `WithValidate`.
Sidecar `/config` and `/dump` require `X-Config-Token`. Plaintext additionally
requires `?unredacted=true` and a matching `X-Unredacted-Token`. Publication times
in `/version.loaded_at` and SSE causes are RFC 3339 strings rather than numbers.

## Transforms and observation

| v0.20.0 | v1.1.0 |
|---|---|
| `WithTransformers(t...)`, `transform.Transformer` | `WithTransform(fns...)`, plain `func(map[string]any) error` values |
| `transform.New(target, migrations...)` | `transform.NewChain(target, migrations...)` |
| `WithMigrations(fn)` | Put the migration first in `WithTransform` |
| `WithRawMapAccess(fn)` | A final read-only transform wrapping `fn` and returning nil |
| `transform.MergeByKey(path, key)` | `WithMergeKeys(map[string]string{path: key})` before transform execution |
| `transform.Defaults`, `SetIfAbsent` | `fc:"default=…"` or `Defaulter` |
| `transform.ExpandLabels` | A label provider or `confmap.ExpandLabels` in a transform; see [labels](sources/labels.md) |
| `WithMetrics`, `WithAuditSink`, `WithDiffReporter` | `WithObserver` with `observe.Metrics`, `observe.JSONLines`, or a `Committed` handler |
| `DiffEvent` | `Committed`: use `Prev`, `Next`, `Cause` and `Diff()` |

Stateless built-in transforms return functions directly; remove their `.Transform`
selector. `RawCapture` retains its `Transform` method. `Chain.Run` returns
`(int, error)`, so wrap it before passing it to `WithTransform`:

```go
fastconf.WithTransform(func(m map[string]any) error {
    _, err := chain.Run(m)
    return err
}, transform.EnvSubst())
```

Observers run synchronously unless wrapped in caller-owned `observe.Async`.
Close the manager before closing an async observer. `Committed` also fires on
initial load (`Prev == 0`); skip that event when replacing an old diff reporter.
Subscriber comparison is unchanged from v0.20.0: callbacks run only when the
extracted value changes. Callbacks now follow registration order.

## Tenant, feature and module changes

- Replace `TenantManager` with an application-owned registry of managers tagged
  by `WithTenant(id)`. Files, env prefixes and credentials must be selected per
  tenant. See the [complete local tenant example](../../examples/tenant/README.md).
- Replace `WithFeatureRules`, root `Eval` and `State.FeatureRules()` with
  `feature.Eval(mgr.Get().Features, key, attrs, fallback)`. The return type is
  `any`; assert the application type. Use `feature.EvalContext` and `feature.Rule`.
- NATS and Redis Streams implementations live in `examples/nats` and
  `examples/redisstream`; copy and adapt them. `integrations/bus` and the
  OpenFeature imitation are removed; the rule evaluator is not an SDK provider.
- `cmd/fastconfgen` is an independent module. Logging adapters, Prometheus and
  OTel retain their leaf module paths; release modules use coordinated version tags.
- Remove `no_provider_*` build tags; Go links only imported packages. Replace
  dotenv replacer exports with `providers/env` equivalents.

Run `go test ./...` in the application and exercise its reload/rollback paths.
Use the [runtime contract](../design/spec.md) and [examples](../../examples/README.md)
for current behavior and configuration. This migration table targets v1.1.0;
changes made afterward are listed in the [changelog](../../CHANGELOG.md#v120).

## Preserve explicit zero values

Tag defaults retain their zero-value semantics. When migrating a switch or limit
that must accept false, 0 or an empty string, remove its default tag and supply
the default through a lower-priority base layer. Review custom Defaults methods
as well. See [the missing/zero/null matrix](processing/field-meta.md#missing-zero-and-null).
Use UnknownError for new struct configurations, or supply WithLogger explicitly
when retaining UnknownWarn during a migration.
