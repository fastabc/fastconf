# Performance Notes

## Baseline environment

Measured on **Apple M2 / darwin arm64 / Go 1.26.2 / `-race=false`** with:

```bash
go test . -run '^$' \
  -bench '^(BenchmarkGet$|BenchmarkGetParallel$|BenchmarkReloadNoop$|BenchmarkReloadCommitSmall$|BenchmarkReloadManySubscribers$|BenchmarkTypedHooksWide$|BenchmarkIntrospectCold$|BenchmarkIntrospectWarmKeys$|BenchmarkExplainDeep$|BenchmarkReloadLarge$)$' \
  -benchmem -count=5
```

Tables below report the median of the five runs from the 2026-05-16 closeout retest.

## Read-path baseline

| Benchmark              | ns/op | B/op | allocs/op |
|------------------------|------:|-----:|----------:|
| `BenchmarkGet`         |  0.52 |    0 |         0 |
| `BenchmarkGetParallel` |  0.14 |    0 |         0 |

`Get()` is a single `atomic.Pointer.Load`. The contract is:

- zero allocation,
- O(1) wall-clock, ~1 ns on the steady state,
- no lock acquisition.

`tools/bench-guard.sh` enforces these guarantees in CI. Any change that violates them MUST add a benchmark regression note and an explicit rationale in the PR description.

## Reload pipeline cost

`BenchmarkReloadNoop` and `BenchmarkReloadCommitSmall` exercise the full

```text
assemble → merge → migration → transform → secret →
typed-hooks → decode → field-meta → validate → policy → commit
```

pipeline over the small reference config. `Noop` keeps the same hash and exits before publish; `CommitSmall` alternates a one-shot override so every run publishes a new `State[T]`.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkReloadNoop` | 15,065 | 13,685 | 174 |
| `BenchmarkReloadCommitSmall` | 16,490 | 14,534 | 179 |
| `BenchmarkReloadLarge` *(256 layers)* | 983,263 | 2,135,027 | 13,406 |

The small-config publish tail is ~1.4 µs over the no-op path on this baseline. Large reloads remain dominated by discovery / decode / merge volume, not the pointer swap.

### Subscriber fan-out retest

`Subscribe` now fires on every committed state and leaves “did the subtree really change?” filtering to the caller. The closeout retest quantifies the fan-out cost with 1 / 10 / 50 subscribers while forcing every reload to commit:

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkReloadManySubscribers/1` | 16,583 | 14,535 | 179 |
| `BenchmarkReloadManySubscribers/10` | 16,813 | 14,615 | 180 |
| `BenchmarkReloadManySubscribers/50` | 17,499 | 14,952 | 180 |

On this baseline, 50 subscribers add ~0.9 µs over the one-subscriber case, with only one extra allocation over the plain commit path. That is small enough to keep the current “caller-side filter” design; no changed-only helper is justified by performance alone yet.

### Cold-path feature probes

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkTypedHooksWide` *(16 `time.Duration` leaves)* | 39,224 | 25,857 | 433 |
| `BenchmarkIntrospectCold` | 1,673 | 1,960 | 28 |
| `BenchmarkIntrospectWarmKeys` | 0.92 | 0 | 0 |
| `BenchmarkExplainDeep` *(32-segment path, 16 origins)* | 219.4 | 2,048 | 1 |

### Active optimisations

- `commit()` caches the most recent `mergedJSON-SHA → state-hash` pair. Idempotent reloads short-circuit the second `json.Marshal` entirely. First reload still pays the marshal cost (cache miss).
- `Subscribe` fires on every committed state — there is no per-field hash table to maintain. The trade-off is "caller-side filter": the callback compares `old` and `new` itself, paying nothing on the reload hot path.

### Cold-path improvement backlog

- Pool the `bytes.Buffer` used by `json.Marshal` (cold path; trivial savings for high-frequency reload deployments).
- If `typed-hooks` becomes a deployment bottleneck, rerun `BenchmarkTypedHooksWide` on a generated hundreds-of-fields shape before optimising; the current benchmark only covers a 16-field representative struct.
- `Explain` currently allocates only for the defensive copy returned to callers. Keep that copy unless a future API offers an explicitly borrowed view.

## Reload pipeline contract

Every stage emits:

- a `slog.Debug` line ("stage done", with duration),
- a `StageDuration(name, dur, ok)` to the metrics sink,
- a tracing span (`fastconf.<stage>`) under the per-reload root span (if a `Tracer` is installed).

Stages remain pure over `*pipelineCtx[T]`; the single-writer reload goroutine is the only goroutine that mutates them. There is no synchronisation between stages.

## Provenance lookup

`OriginIndex.Explain(path)` is a map lookup followed by a defensive copy of the origin chain. Path depth affects the key string, not traversal complexity; chain length controls the returned copy cost. `BenchmarkExplainDeep` above pins the current behavior for a long dotted path with 16 recorded origins.

## Memory shape

`State[T]` holds:

- `*T` — the strongly typed value, freshly allocated per reload.
- The source ref slice.
- The optional `*OriginIndex` (only when `WithProvenance(level)` is configured).
- The lazily-materialised dotted-key view, populated on first `state.Introspect()` access.
- The feature rule table, stamped when `WithFeatureRules[T]` is configured.
- The stamped `SecretRedactor` reference.

The lazy view is the only field that allocates after publish; first access pays one `json.Marshal` + walk, subsequent accesses are O(n) in the number of keys.
