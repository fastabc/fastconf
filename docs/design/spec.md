# FastConf Runtime Contract

This document defines the FastConf v1.1.0 runtime contract.
The root module requires Go 1.24; independent modules declare their own
minimum versions. See [architecture.md](../architecture.md) for package
ownership and [RELEASING.md](../../RELEASING.md) for release checks.

## Reads and writes

`Manager[T]` publishes immutable `State[T]` values through an atomic pointer.
`Get()` is a lock-free, zero-allocation read. Callers must treat its `*T`
(and values reachable through it) as read-only.

`New(ctx, opts...)` performs the first load synchronously, then starts the
reload loop and configured watchers. Its context bounds initialization;
background work retains context values and runs until `Close` or `Shutdown`.
`Load` shares option resolution, typed-hook setup and the initial pipeline
with `New`, without starting background workers.

All later reloads, plans and rollbacks run on one writer goroutine. A caller's
context controls its queued and in-flight work; manager shutdown also cancels
that work. Stages check cancellation at their boundaries. User functions that
do not accept or honor a context must return before the writer can finish.
`Shutdown(ctx)` can time out while the shared close task continues; `Close`
waits for completion.

The writer queue holds 16 requests. Manual operations and coalesced file
reloads wait for queue space or cancellation. Provider watchers drop a new
trigger when the queue is full and emit `EventDropped`; this is separate from
the oldest-error eviction used by `Errors()`.

## Layer order

| Source, lowest precedence first | Ordering within the source class |
|---|---|
| `base/*` | 1000–1999, filename order |
| `overlays/<profile>/*` | 2000–2999, selected directory and filename order |
| `WithAxes` | 3000–6999, relative axis priority then filename order |
| Generators | `RawLayer.Priority`; reported as 7000 + priority |
| Providers | `ProviderInfo.Priority`; reported as 8000 + priority |
| `WithOverride` | Always last; reported as 9000, one reload only |

Files < generators < providers < overrides is a fixed class order. Generator
and provider priorities only order layers within their own class; arbitrary
negative or large priorities cannot cross that boundary. `SourceRef.Priority`
is diagnostic metadata, not a global sort key. Declaration order breaks ties.

File discovery limits each base directory to
1,000 files and each profile or axis directory to 100 files. At most 10 matched
profile directories and 40 declared axes fit in the file bands. Axis priorities
are relative ranks: higher values win, with declaration order breaking ties.
The scanner assigns each axis a separate priority window and rejects overflow.
Root `_meta.yaml` settings live under `spec`; per-overlay `_meta.yaml` uses a
top-level `match` expression. See the [reload pipeline](#reload-pipeline).

The two metadata locations have different shapes. For example, root
`conf.d/_meta.yaml` can contain:

```yaml
spec:
  defaultProfile: prod
  strict: true
  mergeKeys:
    listeners: name
```

The corresponding `conf.d/overlays/prod/_meta.yaml` can contain:

```yaml
match: prod & !dev
```

Root `apiVersion` and `kind` are reserved fields; the current loader does not
validate them or reject unknown metadata fields. `spec.strict: true` enables
strict merging even with `WithStrictMerge(false)`. Explicit merge keys from
`WithMergeKeys` override metadata entries for the same path. A zero `Profile`
still consults the environment and metadata defaults; it does not force base-only
loading. See `Profile` for the selection order.

`contracts.Provider` defines `Name` and `Load(ctx) (Snapshot, error)`.
Providers can also implement `contracts.Watcher` with `Watch(ctx, from)` for
native notifications; load-only providers need no Watch method or watch goroutine.
`Snapshot` carries a map, revision and stale flag. Optional `Describe()` supplies
priority and file-watch paths, including for providers without Watcher. A watcher that cannot
resume a non-empty revision marks its first event with `Gap`. The first Watch
call receives an empty revision; reconnects receive the last non-empty
`Event.Revision`. `Snapshot.Revision` is used for source/commit diagnostics.

## Configuration and ownership

Configure providers before registering them with `WithProvider`. Fluent methods
such as `WithPriority`, `WithCoerce` and `At` mutate and return the same provider.
Do not call them concurrently with loading or watching. `Describe` is read once
at registration; changing a provider's priority afterward does not reorder it.
Ownership is constructor-specific: `source.NewBytes` copies its bytes, whereas
`cliflag.New` retains its input map, which callers must stop mutating.

| Option behavior | Examples |
|---|---|
| Last call replaces the setting | `WithDir`, `WithProfile`, `WithDecodeFormat` |
| Calls accumulate in order | `WithProvider`, `WithGenerator`, `WithTransform`, `WithValidate` |
| Entries merge by key; the last value wins | `WithMergeKeys` |

`WithProvider`, `WithTypedHook` and `WithValidate` ignore nil entries.
`WithTransform` rejects nil functions at construction; `WithLogger` and
`WithTracer` reject nil values. These contracts are specific to each option.

## Decoding

| Stage | API | Meaning |
|---|---|---|
| Source bytes to configuration tree | `contracts.Decoder` | Decode documents; YAML, JSON, TOML and INI are built in |
| Tree values to target field types | `codec.TypedHook` / `WithTypedHook` | Convert values such as duration strings before struct decoding |
| Tree to application struct | `WithDecodeFormat(JSON/YAML)` | Select the JSON (default) or YAML bridge and its struct-tag rules |

Source format and target decoding are independent: a TOML document can use the
default JSON bridge. `WithDecodeFormat(YAML)` honors YAML tags when loading and
hashing `T`. Diagnostic views (`Map`, `Dump`, `Diff`, including Plan and commit
diffs) still use JSON field names and omit `json:"-"` fields. A YAML-only field
change can therefore publish a new snapshot with an empty diagnostic diff.
Map trees are also public through
provider snapshots, transforms and `State.Map`; they are not solely internal.

INI input: default-section keys map to the root,
other sections to child maps, and leaf values remain strings until typed hooks.
Strict merging checks raw types before those hooks, so an INI string cannot
override a YAML number in strict mode. See the [INI recipe](../cookbook/ini.md)
for syntax and compatibility details; INI is not a dump or struct-tag format.

## Reload pipeline

```text
assemble → merge → transform → secret → typed-hooks → decode
         → field-meta → validate → policy → commit
```

Assembly discovers files, invokes generators and loads providers. Merge
applies deep/strategic merge and RFC 6902 patches in layer order. Transforms
include optional schema migration functions. Secret resolution precedes typed
hooks and decode. Decode applies struct-tag defaults and then `Defaulter`.
Field metadata, user validators and policies run before publication.

Any pipeline failure preserves the current snapshot and generation. Later
reload and plan failures also go to `Errors()` (capacity 16, oldest error
dropped on overflow); initialization failures are returned by `New`/`Load`.
Context cancellation remains recognizable with `errors.Is`. The eight main
pipeline error categories are available in the root API reference; history has
additional `ErrHistoryDisabled` and `ErrUnknownGeneration` errors.

Commit hashes the final typed value encoded in the selected decode format.
Both encoders sort map keys. Custom `MarshalJSON` / `MarshalYAML` implementations
must produce deterministic output and include every relevant configuration
field; returning a YAML node preserves its supplied order. Marshaling errors
abort publication and preserve the previous snapshot.
Equal hashes do not publish or notify
subscribers. File-only inputs may skip the pipeline under the constraints in
[Performance Notes](perf.md). A published change increments generation, stores
the previous snapshot in enabled history, then emits `Committed` and calls
subscribers. `Subscribe` compares extracted values before invoking a callback. Subscriber
and reload/commit observer callbacks run on the writer. They must not wait on
that manager's `Reload`, `Plan`, rollback, `Close` or `Shutdown`; hand those
operations to the owner or another goroutine.

`Plan` runs the pipeline without publication and collects validator and policy
findings, preserving policy severity. `History().Rollback` republishes a
retained snapshot as a new generation without rerunning validation.
Plan can return nil error with failing validator reports or error-severity policy
findings; inspect both collections before treating a preview as acceptable.
Reload loads sources again, so a preview does not reserve its exact candidate.
Plan still invokes providers, generators and user hooks, whose external effects
are not rolled back. It accepts PlanOption values, not ReloadOption values such
as `WithOverride`.

## Watchers and observation

`WithWatch` enables parent-directory file watching, including Kubernetes
symlink swaps. The default `ProfileK8s` coalesces per directory with a 30 ms
quiet window, 250 ms maximum lag and 5 ms swap hint. Provider watchers run
independently of the file-watch switch. `Pause` suppresses watcher-triggered
reloads; explicit `Reload`, `Plan` and rollback remain available.

Observers receive reload, stage, provider, drop and commit events. Reload
and commit callbacks are synchronous; provider notifications may be concurrent.
`WithObserverTimeout` cancels each callback context after 2 seconds by default;
it cannot interrupt a callback that ignores cancellation. `observe.Async`
provides a bounded queue with a dropped-event count. Its owner must call
`Close()`; closing a manager does not close separately created observers.
Tracing exposes the reload, assemble, pipeline-stage and commit spans.

## Diagnostic views

`State.Map`, `Dump`, `Diff`, `Explain` and plan/observer diffs mask
`fc:"secret"` fields and `WithSecretPaths` matches. A provenance container
with secret descendants is masked as a whole, including historical values
absent from the final snapshot. Secret changes are detected before masking.
`Unredacted().Map/Dump` are the explicit plaintext views. `Value` and `Get`
also expose the typed plaintext configuration for business code.

Returned diagnostic containers, source slices and cause revision maps are
copies. JSON struct tags govern snapshot view names. The first diagnostic
view caches a JSON tree for that snapshot; history can retain these trees.

The sidecar masks `/config` and `/dump` by default. Plaintext needs both
`?unredacted=true` and `X-Unredacted-Token` matching `-unredacted-token`.

### Event timestamps

`ReloadCause.At`, `ReloadError.At`, and `contracts.Event.At` use `time.Time`.
Commit and rollback record publication time; Plan records preview time without
publishing a snapshot. JSON uses RFC 3339 strings with optional fractional
seconds, including sidecar `/version.loaded_at` and SSE cause `At`.
