# FastConf Runtime Contract

This document describes the v1.0 API implemented by the current checkout.
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

| Source | Effective priority |
|---|---|
| `base/*` | 1000–1999, filename order |
| `overlays/<profile>/*` | 2000–2999, selected directory and filename order |
| `WithAxes` | 3000–6999, relative axis priority then filename order |
| Generators | 7000–7999 |
| Providers | 8000–8999, ordered by optional `contracts.Describer` metadata |
| `WithOverride` | 9000, one reload only |

Declaration order breaks ties. File discovery limits each base directory to
1,000 files and each profile or axis directory to 100 files. At most 10 matched
profile directories and 40 declared axes fit in the file bands. Axis priorities
are relative ranks: higher values win, with declaration order breaking ties.
The scanner assigns each axis a separate priority window and rejects overflow.
Root `_meta.yaml` settings live under `spec`; per-overlay `_meta.yaml` uses a
top-level `match` expression. See the [reload pipeline](#reload-pipeline).

`contracts.Provider` defines `Name`, `Load(ctx) (Snapshot, error)` and
`Watch(ctx, from)`. `Snapshot` carries a map, revision and stale flag. Optional
`Describe()` supplies priority and file-watch paths. A watcher that cannot
resume a non-empty revision marks its first event with `Gap`.

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

Commit hashes the final typed value. Equal hashes do not publish or notify
subscribers. File-only inputs may skip the pipeline under the constraints in
[Performance Notes](perf.md). A published change increments generation, stores
the previous snapshot in enabled history, then emits `Committed` and calls
subscribers. `Subscribe` compares extracted values before invoking a callback.

`Plan` runs the pipeline without publication and collects validator and policy
findings, preserving policy severity. `History().Rollback` republishes a
retained snapshot as a new generation without rerunning validation.

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
