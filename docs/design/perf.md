# Performance Notes

## Read-path contract

`Manager.Get()` reads an atomic snapshot pointer: O(1), lock-free, zero
allocations. `tools/bench-guard.sh` checks `BenchmarkGet` in CI with a default
limit of 5 ns/op and 0 allocs/op. Local timings depend on CPU, Go version,
concurrency and instrumentation; they are not a portable latency guarantee.

## Reproduce measurements

Run from the repository root with a clean workspace and record the commit,
Go version, OS, architecture and `GOMAXPROCS` alongside the samples:

```bash
go version
go env GOOS GOARCH
GOWORK=off GOMAXPROCS=4 go test . -run '^$' \
  -bench '^(BenchmarkGetWarmState|BenchmarkReloadNoop|BenchmarkReloadCommitSmall|BenchmarkReloadLarge|BenchmarkStateMapCold)$' \
  -benchmem -count=5
bash tools/bench-guard.sh
bash tools/profile-reload.sh /tmp/fastconf-profiles
```

`BenchmarkReloadNoop` reloads unchanged files. `BenchmarkReloadCommitSmall`
changes an override on each iteration and verifies generation advances.
`BenchmarkReloadLarge` uses 256 file layers; `BenchmarkReloadLargeIncremental`
changes one layer. `BenchmarkStateMapCold` measures a snapshot's first masked
map view. Keep race-detector timings separate from performance samples.

## Input fingerprint

After assembly, file-only inputs are fingerprinted using file contents,
paths, priority, profile, codec, root metadata and codec registry generation.
An equal fingerprint skips merge through hashing and avoids cloning cached
file trees. Assembly borrows immutable decoded layers; merge clones them
before any mutable pipeline stage. Files are still read on each reload:
neither size nor mtime determines freshness.

The shortcut is disabled by providers, generators, overrides, transforms,
secret resolvers, custom typed hooks, validators, policies, observers,
`Defaulter`, or custom JSON/YAML/text encoding methods anywhere in `T`.
These hooks may depend on external state. Rollback clears the fingerprint;
failed reloads do not update it. Plans always execute their pipeline.

## Retained memory

Each manager caches the latest successfully decoded version for at most 512
file path/codec pairs. Content hash and codec registry generation validate a
hit. Reverting a changed file decodes it again; failed decodes do not replace
the last successful entry. New paths use FIFO eviction, bounded by entry
count rather than bytes, so retained memory depends on document sizes.

Snapshots lazily cache one immutable JSON tree for diagnostics. Each returned
view is detached, including custom redactor inputs. `Get` does not create
this tree. History and application-held snapshots extend its lifetime.
Managers compile immutable redaction metadata once, shared by their snapshots,
plans and history. Types without static secrets skip that traversal; configured
path rules still apply. Real JSON paths and conservatively flattened provenance
paths use separate compiled rules. Interface configurations compile runtime
type metadata when needed. `Explain` copies the origin chain; origin containers
with secret descendants are masked as a whole.

Unknown-field warnings retry decoding into a fresh target using the same
encoded bridge bytes. Both JSON and YAML retain the strict/loose decode
order; retries never reuse a partially decoded target.

The final typed value is hashed after decode/defaults whenever the pipeline
runs. Subscriber equality avoids unnecessary callbacks. RFC 6902 patching
works on a copy of the merged map, retaining untouched leaf types.

## Resource soak

```bash
GOWORK=off GOMAXPROCS=4 FASTCONF_SOAK_DURATION=5m \
  go test -mod=readonly -race -count=1 -timeout=10m \
  -run '^TestResourceSoak$' -v ./tests/soak
```

The soak covers reloads, plans, file caching, history, observers and shutdown.
The manual resource-soak workflow runs the platform matrix. Record actual
platform results for each candidate; earlier runs do not validate a new one.

## Reproducible comparison

The standalone [comparison project](../../benchmarks/compare/README.md) includes
equivalent validated snapshot adapters, direct struct/atomic baselines, three
fixture sizes, file/env startup, concurrent reads and separate latency samples.
Dependencies and raw reports stay outside the released modules.

## 2026-10-25 optimization measurements

Apple M5 Pro, Darwin/arm64, Go 1.26.10, GOMAXPROCS=4, no race.
The baseline is `03e6a95`, exported to a disposable checkout with the same
benchmark fixture file as the optimized working tree. Each timing is the median
of three 200 ms runs, executed sequentially after correctness and lint checks.
These are local observations, not portable latency guarantees.

| Scenario | Before ns/op | After ns/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Warm Get | 0.2264 | 0.2346 | 0 | 0 | 0 | 0 |
| File noop reload | 2,762 | 2,672 | 3,464 | 2,920 | 34 | 30 |
| 256-layer noop reload | 143,289 | 113,217 | 282,666 | 196,777 | 1,576 | 1,064 |
| 256-layer incremental reload | 285,009 | 263,984 | 456,423 | 449,902 | 3,770 | 3,735 |
| Cold Map (internal snapshot) | 2,297 | 1,291 | 6,273 | 2,784 | 60 | 30 |
| Secret Map (256 items, warm) | 169,309 | 37,787 | 433,026 | 98,632 | 5,031 | 1,184 |
| Empty recursive type, cold ApplyJSON | 360 | 800 | 1,320 | 1,816 | 14 | 29 |
| UnknownWarn, JSON | 1,220 | 977 | 2,281 | 2,137 | 27 | 22 |
| UnknownWarn, YAML | 16,962 | 10,424 | 77,156 | 46,260 | 327 | 220 |
| Deep Diff, equal | 49,626 | 29,402 | 35,464 | 4,264 | 552 | 5 |
| Deep Diff, one leaf changed | 48,826 | 29,717 | 36,184 | 4,552 | 561 | 8 |
| Deep Diff, 32 leaves changed | 56,026 | 32,848 | 61,688 | 16,664 | 814 | 75 |
| Reload with commit observer | 17,680 | 13,017 | 22,986 | 16,045 | 282 | 213 |
| Reload with 1 subscriber | 7,162 | 7,810 | 8,011 | 8,142 | 107 | 107 |
| Reload with 50 subscribers | 9,068 | 8,879 | 8,909 | 9,037 | 108 | 108 |
| Subscribe/cancel, 50 stable subscribers | 60.89 | 57.38 | 88 | 88 | 3 | 3 |
| Cache-only 256 KiB version alternation | 126 | 744,570 | 336 | 1,309,521 | 2 | 24 |

The cold recursive-type sample used the then-available ApplyJSON helper on an
empty map, including metadata construction. Its current production equivalent is
Compile(...).Apply(...), measured by BenchmarkRedactionPlanBuild. The table above
retains its historical measurements. Published snapshots share the compiled plan;
this initialization cost is deliberately reported separately from warm views.
Ordinary committed reloads use about 128 B/op more in these samples; the
layer/snapshot metadata is larger. These changes do not improve every workload.

For 32 successive versions of one 256 KiB JSON document, forced-GC live heap
increased by a median 8,410,064 B before and 263,376 B after. This is a cache
retention sample, not whole-process RSS or a long-duration leak test. The
cache-only alternation case above intentionally revisits two historical versions:
it excludes reading, hashing, merging and publication, and shows the cost of
re-decoding discarded versions. History.Rollback restores retained snapshots
and does not use this file cache.

A temporary ordered subscriber snapshot prototype was rejected: Subscribe/cancel
at 50 subscribers rose from about 58 ns / 88 B / 3 allocations to 2 microseconds /
1,752 B / 7 allocations, for limited stable-publication benefit. The existing
map and publication-time sort remain.

Disposable workspace preparation (`bash tools/modules.sh exec true`) fell from
a median 1.484 s to 0.153 s over three sequential runs. The optional examples
now share one preparation. This excludes example execution time.

Validation covered all 20 candidate modules and all 20 local-workspace modules
with race enabled, 21-module lint, make check, shellcheck/actionlint and all
examples. Final root changes additionally passed root race and lint; hostname
and Vault cases passed 10 shuffled race runs. Diagnostic fuzz ran for 30 seconds
on the final target. A separate 10-second resource smoke covered one 4 KiB cycle;
the five-minute multi-size/platform soak was not run for this change.

Raw optimization samples and profiles are local under
`/tmp/fastconf-implementation-20261025/`. The corresponding dated cross-library
comparison is recorded in the [comparison report](../../benchmarks/compare/REPORT.zh.md).

## 2026-11-01 reliability and lean-refactor measurements

Apple M5 Pro, Darwin/arm64, Go 1.26.10, GOMAXPROCS=4, no race.
Baseline `ede6d00` and the refactored checkout use the same benchmark fixtures.
Root samples below are medians of three sequential 200 ms runs after correctness
checks. They are local observations, not portable latency guarantees.

| Scenario | Before ns/op | After ns/op | Before B/op | After B/op | Before / after allocs |
|---|---:|---:|---:|---:|---:|
| Warm Get | 0.2269 | 0.2203 | 0 | 0 | 0 / 0 |
| File noop reload | 2513 | 2599 | 2920 | 2968 | 30 / 31 |
| 4 KiB noop reload | 4242 | 4272 | 7736 | 7784 | 30 / 31 |
| 64 KiB noop reload | 30431 | 30212 | 76600 | 76648 | 30 / 31 |
| 1 MiB noop reload | 359004 | 352750 | 1.05964e+06 | 1.05969e+06 | 30 / 31 |
| Commit, 1 subscriber | 7744 | 7305 | 8141 | 8187 | 107 / 108 |
| Commit, 10 subscribers | 7883 | 7465 | 8301 | 8348 | 108 / 109 |
| Commit, 50 subscribers | 9158 | 8839 | 9038 | 9084 | 108 / 109 |
| Cold Map | 1245 | 1306 | 2816 | 2816 | 30 / 30 |
| Commit with observer | 13331 | 12767 | 16049 | 16095 | 213 / 214 |

Bounded discovery initially increased 1 MiB noop reads from about 1.06 MB to
2.23 MB/op through io.ReadAll growth. The final reader preallocates from stat size,
but bounds every read at 4 MiB+1 and reads beyond stale size hints. Growth,
shrinkage, inaccurate hints and read errors are covered. Missing optional metadata
bypasses error-string formatting. The remaining discovery overhead is about
48 B and one allocation per file reload in this fixture. Get remains allocation-free.

The comparison module's 1 MiB MapWarm case needs separate attention: five 500 ms
samples had median 771721 → 861481 ns/op (11.6% slower),
with 2.88394e+06 B/op and 16388 allocations unchanged. Earlier sequential samples
also showed a slowdown. The warm view/clone execution path was not changed;
the cause is unresolved, so this change does not claim an overall performance gain.
The allocation profile still attributes most space to DeepClone/CloneValue.

Reproduce the root table in each checkout, sequentially:

```bash
GOPROXY=direct GOWORK=off GOMAXPROCS=4 go test . -run '^$' \
  -bench '^(BenchmarkGetWarmState|BenchmarkStateMapCold|BenchmarkReloadNoop|BenchmarkReloadManySubscribers|BenchmarkReloadCommitObserver|BenchmarkReloadDocument)$' \
  -benchmem -benchtime=200ms -count=3
```

For MapWarm:

```bash
GOPROXY=direct GOWORK=off GOMAXPROCS=4 go -C benchmarks/compare test -run '^$' \
  -bench '^BenchmarkFastConfDiagnostics$/1024KiB/MapWarm$' \
  -benchmem -benchtime=500ms -count=5
```
