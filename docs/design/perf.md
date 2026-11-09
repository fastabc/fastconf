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
export GOPROXY=direct
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
An equal fingerprint skips file decoding as well as merge through hashing.
Assembly fingerprints discovered bytes before obtaining decoded layers from the
cache; equal eligible inputs bypass decoding. Source decode failures remain
assembly failures for logging and stage observation. Merge clones cached trees before any mutable
pipeline stage. Files are still read on each reload:
neither size nor mtime determines freshness.

The shortcut is disabled by providers, generators, overrides, transforms,
secret resolvers, custom typed hooks, validators, policies, observers,
`Defaulter`, or custom JSON/YAML/text encoding methods anywhere in `T`.
These hooks may depend on external state. Rollback clears the fingerprint;
failed reloads do not update it. Plans always execute their pipeline.

## Retained memory

Each manager caches the latest successfully decoded version for at most 512
file path/codec pairs, with a 16 MiB budget on the sum of their original source
document sizes. This is an admission weight, not a decoded-heap or RSS limit:
codecs can expand input, and maps add overhead. Content hash and codec registry
generation validate a hit. Reverting a changed file decodes it again; failed
decodes do not replace the last successful entry. Before decoding a scan, entries
for absent paths are released. Active entries are retained; excess paths are
decoded without admission, avoiding sequential-scan thrashing above capacity.
An oversized successful replacement releases its obsolete entry but is not
cached. Source bytes themselves are not retained by the cache.

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
runs. Equal values still publish when resolver-sensitive paths change; these
metadata commits advance history/generation and emit Committed with an empty
value diff, without invoking subscribers. Other source/provenance-only changes
retain the previous committed snapshot when the value and sensitive paths match.
Subscriber equality avoids unnecessary value callbacks. RFC 6902 patching
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
comparison remains in Git history; the current [comparison report](../../benchmarks/compare/REPORT.zh.md)
records the latest measured source snapshot.

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

## 2026-11-08 实施期间的宽配置解码测量（历史记录）

Apple M5 Pro，darwin/arm64，Go 1.26.10，默认 GOMAXPROCS=18，非 race，
每种模式 6 个样本，以下为中位数。`BenchmarkReloadWide` 使用一个 provider，
每次加载 100 section × 100 int 叶子，保证执行完整管线，包含 provider 层的复制成本。

| 模式 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| 默认 encoding/json | 4,103,752 | 3,565,308 | 62,422 |
| GOEXPERIMENT=jsonv2 | 3,412,446 | 2,963,462 | 32,175 |

复现命令：

```sh
GOPROXY=direct go test -run '^$' -bench BenchmarkReloadWide -benchmem -count=6 .
GOPROXY=direct GOEXPERIMENT=jsonv2 go test -run '^$' -bench BenchmarkReloadWide -benchmem -count=6 .
GOPROXY=direct make test-jsonv2
```

本次另用相同树做临时 JSON marshal/unmarshal 微基准：默认桥接 2.56 ms / 1,986,132 B / 41,740 allocs，
jsonv2 为 1.89 ms / 1,186,188 B / 21,436 allocs。桥接约占默认完整 reload 的 62%，
该微基准分配下降约 49%。

设计阶段另一组 10k 叶子结构的测量为：桥接约占 60% 时间，jsonv2 使桥接分配
从约 24,060 降为 4,061（约 83%）。这些数字依赖目标结构、map/struct 比例及测量边界，
不能把该分配降幅直接当成当前 map-of-map 全管线的结果。
本次结果同样支持继续使用标准 JSON 桥，不实现流式解码器；待 `encoding/json/v2`
脱离 GOEXPERIMENT 后再评估切换。CI 在 Go 1.26 上运行实验模式全量根模块测试，
以持续验证 unknown-field 识别及桥接语义。

## 2026-11-08 FastConf v1.6.0 复测

本轮将当前源码定义为 **FastConf v1.6.0**，被测提交为
`d0364e0cc7247463f34156723dfc2c59f8338615`。Apple M5 Pro，Darwin 27.0.0 / arm64，
Go 1.26.10，固定 GOMAXPROCS=4、GOGC=100，GOEXPERIMENT 默认为空，不使用 race 计时。
根模块及其包共 31 组基准，每组 6 次、benchtime=500ms，共 186 条样本。
表格使用全部样本的中位数，不删除离群值；原始样本和 benchstat 区间均保留。

根模块正确性测试及比较适配器的 race 检查在计时前通过。
本次任务的构建、race 和各套 benchmark 串行执行；系统及其他应用仍可能产生调度噪声。
历史章节保留原样，不将不同日期、源码或采样时长的差异直接归因于某项代码改动。

### 根模块结果

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkGetWarmState` | 0.2432 | 0.2535 | 0 | 0 | 0 / 0 |
| `BenchmarkReloadNoop` | 2,799 | 2,869 | 2,968 | 2,976 | 31 / 31 |
| `BenchmarkReloadCommitSmall` | 7,683 | 8,276 | 111 |
| `BenchmarkReloadCommitYAML` | 11,026.5 | 27,321 | 165 |
| `BenchmarkReloadDocument/4KiB` | 4,315.5 | 7,784 | 31 |
| `BenchmarkReloadDocument/64KiB` | 30,282 | 76,648 | 31 |
| `BenchmarkReloadDocument/1024KiB` | 368,753 | 1,059,688 | 31 |
| `BenchmarkSubscribeContention` | 13,843 | 13,576 | 10,159 | 10,199 | 115 / 115 |
| `BenchmarkReloadManySubscribers/1` | 8,396 | 8,544 | 8,276 | 8,316 | 111 / 111 |
| `BenchmarkReloadManySubscribers/10` | 8,540 | 8,867 | 8,438 | 8,477 | 112 / 112 |
| `BenchmarkReloadManySubscribers/50` | 10,049 | 10,240 | 9,175 | 9,214 | 112 / 112 |
| `BenchmarkTypedHooksWide` | 2,629 | 3,080 | 31 |
| `BenchmarkStateMapCold` | 1,394.5 | 2,880 | 30 |
| `BenchmarkReloadCommitObserver` | 14,035 | 16,505 | 222 |
| `BenchmarkRedactionPlan` | 38,731 | 98,632 | 1,184 |
| `BenchmarkRedactionPlanBuild` | 929 | 1,896 | 30 |
| `BenchmarkUnknownWarnDecode/YAML=false` | 944.4 | 2,137 | 22 |
| `BenchmarkUnknownWarnDecode/YAML=true` | 10,863.5 | 46,259.5 | 220 |
| `BenchmarkDiffDeep/equal` | 30,560 | 4,264 | 5 |
| `BenchmarkDiffDeep/sparse` | 30,824.5 | 4,552 | 8 |
| `BenchmarkDiffDeep/dense` | 34,612.5 | 16,664 | 75 |
| `BenchmarkSubscriberChurn` | 61.16 | 60.51 | 88 | 88 | 3 / 3 |
| `BenchmarkLayerCacheVersions` | 55,807,823 | 84,918,082 | 1,098 |
| `BenchmarkLayerCacheAlternating` | 708,295 | 1,309,522 | 24 |
| `BenchmarkGet` | 0.3203 | 0 | 0 |
| `BenchmarkGetParallel` | 0.1116 | 0 | 0 |
| `BenchmarkReloadLarge` | 135,626 | 209,064 | 1,320 |
| `BenchmarkReloadLargeIncremental` | 318,721 | 329,970 | 462,471 | 462,493 | 3,994 / 3,994 |
| `BenchmarkReloadWide` | 4,351,902 | 4,462,527 | 3,613,232 | 3,638,156 | 62,427 / 62,429 |
| `BenchmarkFeatureEval` | 34.78 | 0 | 0 |
| `BenchmarkExplainDeep` | 285.3 | 2,048 | 1 |

`Get` / `GetWarmState` 是不同夹具的紧循环吞吐量，不是业务请求延迟。
本轮单独运行的 `bench-guard` 也通过：0.3189 ns/op，0 B/op，0 allocs/op。
`GetParallel` 是并行吞吐量，不能解释为一次独立读取的尾延迟。

`ReloadNoop`、`ReloadDocument` 和未改变文件的 `TypedHooksWide` 可以命中输入指纹，
因此 `TypedHooksWide` 不能当作每次都执行 typed hooks 的计时。
`ReloadLarge` 为 256 层文件无变化重载；`ReloadLargeIncremental` 每次改变一层；
`ReloadWide` 使用 Provider，强制完整管线；几者的成本边界不同。

`LayerCacheVersions` 的一次操作包含同一个 256 KiB 文档的 32 个版本及 GC，
六次 `live-B` 的中位数为 263,080 B。它是 GC 后保留堆的净增，
不是上表的累计分配 B/op，也不是进程 RSS。`LayerCacheAlternating` 只测缓存取值/重解码，
不包含文件读取、合并与发布。`RedactionPlanBuild` 的初始化成本与共享计划的 warm Map 分开计量。

### 宽配置 JSON 实验对照

以下两种模式与根模块数据使用相同 4 个 P、6 次 500ms 采样。
每次完整 reload 加载 100 个 section × 100 个 int 叶子。

| 模式 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| 默认 JSON | 4,128,289.5 | 3,624,536 | 62,428 |
| GOEXPERIMENT=jsonv2 | 3,638,195.5 | 2,942,495 | 32,175.5 |

本轮 jsonv2 的耗时中位数低 11.9%，分配字节低 18.8%，
分配次数低 48.5%。这里只比较完整管线，未重测桥接独立微基准；
上节实施期间的 18 个 P 和临时桥接数据仍是历史记录，不与本轮混算。
本次数据不改变继续使用标准 JSON 桥、等待 json/v2 稳定后再评估切换的结论。

### 复现与原始数据

```bash
export GOPROXY=direct GOWORK=off GOMAXPROCS=4 GOGC=100 GOEXPERIMENT=
go test -mod=readonly -run '^$' -bench . -benchmem -benchtime=500ms -count=6 ./...
GOEXPERIMENT=jsonv2 go test -mod=readonly -run '^$' \
  -bench '^BenchmarkReloadWide$' -benchmem -benchtime=500ms -count=6 .
bash tools/bench-guard.sh
```

本地证据目录为 `/tmp/fastconf-benchmarks-20261108-d0364e0/`，包括环境、正确性结果、
`root-raw.txt`、`root-summary.txt`、JSON 实验原始数据及摘要、只读路径 guard 结果。
原始数据不进入发布模块。跨库比较的基准、独立延迟和 RSS 见
[v1.6.0 比较报告](../../benchmarks/compare/REPORT.zh.md)。

## 2026-11-09 Reload 一致性与缓存边界优化

Apple M5 Pro，Go 1.26.10，darwin/arm64，GOMAXPROCS=4，GOGC=100；
基线 `48b700e` 与本次工作区，使用同一夹具，各三个 300ms 样本取中位数，串行非 race。

| Benchmark | 基线 ns/op | 最终 ns/op | 基线 B/op | 最终 B/op | 基线/最终 allocs |
| --- | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkGetWarmState` | 0.2432 | 0.2535 | 0 | 0 | 0 / 0 |
| `BenchmarkReloadNoop` | 2,799 | 2,869 | 2,968 | 2,976 | 31 / 31 |
| `BenchmarkSubscribeContention` | 13,843 | 13,576 | 10,159 | 10,199 | 115 / 115 |
| `BenchmarkReloadManySubscribers/1` | 8,396 | 8,544 | 8,276 | 8,316 | 111 / 111 |
| `BenchmarkReloadManySubscribers/10` | 8,540 | 8,867 | 8,438 | 8,477 | 112 / 112 |
| `BenchmarkReloadManySubscribers/50` | 10,049 | 10,240 | 9,175 | 9,214 | 112 / 112 |
| `BenchmarkReloadCacheBoundary/layers=512/full=false` | 296,881 | 291,795 | 421,929 | 421,937 | 2,602 / 2,602 |
| `BenchmarkReloadCacheBoundary/layers=512/full=true` | 669,239 | 687,324 | 870,418 | 869,993 | 7,792 / 7,792 |
| `BenchmarkReloadCacheBoundary/layers=513/full=false` | 1,717,814 | 295,385 | 4,184,705 | 422,089 | 25,170 / 2,607 |
| `BenchmarkReloadCacheBoundary/layers=513/full=true` | 2,116,903 | 749,917 | 4,655,990 | 878,413 | 30,388 / 7,853 |
| `BenchmarkSubscriberChurn` | 61.16 | 60.51 | 88 | 88 | 3 / 3 |
| `BenchmarkReloadLargeIncremental` | 318,721 | 329,970 | 462,471 | 462,493 | 3,994 / 3,994 |
| `BenchmarkReloadWide` | 4,351,902 | 4,462,527 | 3,613,232 | 3,638,156 | 62,427 / 62,429 |

这些是本机短基准的中位数，不能把几个百分点的波动认定为稳定提升。Get 的亚纳秒值是紧循环吞吐，不是业务调用延迟；最终保持零分配。主要收益来自超过缓存容量时避免全量解码，宽配置 JSON 桥没有改写。

复现（两个工作区使用相同的 `cache_boundary_bench_test.go`）：

```bash
export GOPROXY=direct GOWORK=off GOMAXPROCS=4 GOGC=100
go test -mod=readonly -run '^$' \
  -bench '^(BenchmarkReloadCacheBoundary|BenchmarkGetWarmState|BenchmarkReloadNoop|BenchmarkReloadLargeIncremental|BenchmarkReloadWide|BenchmarkReloadManySubscribers|BenchmarkSubscriberChurn|BenchmarkSubscribeContention)$' \
  -benchmem -benchtime=300ms -count=3 .
```
