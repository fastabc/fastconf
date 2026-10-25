# Reproducible configuration comparison

This standalone module compares FastConf with Viper, Koanf, Cleanenv and
Envconfig. [go.mod](go.mod) pins the comparison dependencies; these are fixed
comparison versions, not a claim that each is the latest release. Its `replace` selects the local FastConf
source. It is excluded from release module discovery, tags and candidate archives;
competitor dependencies do not enter the root go.mod.

Run correctness separately from performance, on Go 1.26+ (the pinned benchstat
tool requires 1.26):

```sh
cd benchmarks/compare
GOWORK=off go test -race ./...
bash run.sh results/local
```

The default run uses GOMAXPROCS=4, GOGC=100, 10 benchmark repeats at 1 second and
100 individual latency samples per adapter/size/operation. For a shorter run:

```sh
COUNT=5 BENCHTIME=200ms SAMPLES=100 bash run.sh results/local
```

Use an idle machine. Do not overlap builds, race tests or other benchmarks. The
runner records the OS, CPU, toolchain, module versions, base commit, working diff,
source hashes, fixture hashes, raw Go output, pinned benchstat summary and raw
individual latencies. Each latency process also gets `/usr/bin/time` peak RSS
output (Darwin bytes, Linux KiB). This is whole-process peak RSS including fixture
construction and runtime setup, not retained configuration size or per-operation
allocation. The entire `results/` directory is ignored; generated artifacts stay
local and are not committed to Git.

## Workloads

| Measurement | Contract and scope |
|---|---|
| Startup | Bytes already in memory → fresh typed value → shared Counter/Mirror validation; FastConf uses Load without background workers |
| ValidReload | Alternate two valid inputs → decode, validate, hash and publish a fresh atomic snapshot |
| NoopReload | Same bytes → no new snapshot; all dynamic adapters compare canonical JSON hashes |
| InvalidReload | Counter=0 fails the shared validator; retain the exact previous pointer |
| HotRead | Direct struct, atomic.Pointer, FastConf Get, and Viper/Koanf decoded struct + atomic adapters |
| ConcurrentRead | Parallel readers, one writer with a 1 ms ticker, 4 KiB input; readers check Counter=Mirror |
| FileStartup | A shared os.ReadFile + the same startup adapter; warm OS cache, no fsnotify or remote network |
| EnvStartup | The same two environment variables into FastConf, Cleanenv and Envconfig, with equivalent validation |
| FastConfDiagnostics | Warm Map, and valid Reload with history=8 plus cold Map; no cross-library comparison |

Fixtures are generated deterministically by `Fixture`: exactly 4 KiB / 64 KiB /
1 MiB, one YAML layer, respectively 32 / 512 / 8192 list entries, two related
integer fields and padding. Valid updates change the two related fields together;
list cardinality and names stay fixed. Generation and file writes are outside the
timed regions. Tests assert byte length, adapter equality, noop pointer identity,
failed-update retention and successful publication. Fixture SHA-256 values are
in correctness.txt.

FastConf parses through a mutable in-memory Provider; it has no file-input
fingerprint shortcut here. Viper/Koanf adapters create a fresh parser instance
for each candidate to avoid leftover keys, then implement application-level
validation, hash deduplication and atomic publication. Their setup and hash costs
are included. Their hot reads operate on those published structs, not string-key
lookups. The direct-struct baseline also represents static Cleanenv/Envconfig
reads. These libraries do not participate in dynamic snapshots they do not
implement; Kustomize is outside the application-runtime workload.

`ConcurrentRead` reports throughput, not tail latency, and includes writer
allocations in the process-wide allocation counters. A 1 ms ticker requests
updates; actual completed update rate depends on workload and scheduling.
The standalone sample tool records individual latencies and nearest-rank
p50/p95/p99 after an initial load and GC. With 100 samples p99 is only the 99th
sample, unsuitable for a service SLO. No race instrumentation is used for timing.
The startup Cleanenv case uses ParseYAML (no implicit env override); env parsing
is measured separately. FastConf/Viper/Koanf check unknown fields; Cleanenv's YAML
parser does not, and fixtures contain no unknown keys.

## API sources

Adapters follow the pinned projects' own APIs and source:

- [Viper 1.21.0](https://github.com/spf13/viper/blob/v1.21.0/viper.go): New, ReadConfig, UnmarshalExact.
- [Koanf 2.3.0](https://github.com/knadh/koanf/blob/v2.3.0/koanf.go): Load with a map Provider, UnmarshalWithConf and ErrorUnused.
- [Cleanenv 1.5.0](https://github.com/ilyakaznacheev/cleanenv/blob/v1.5.0/cleanenv.go): ParseYAML and ReadEnv.
- [Envconfig 1.4.0](https://github.com/kelseyhightower/envconfig/blob/v1.4.0/envconfig.go): Process.

The installed module source was read to verify those signatures. See
[compare.go](compare.go), [benchmarks](compare_test.go) and
[sample tool](cmd/sample/main.go) for the complete measured work.

Measured results: [2026-10-25 report (中文)](REPORT.zh.md), including raw samples
and limitations. That report describes its recorded source and environment, not
the performance of every later checkout.

Related: [runtime performance contract](../../docs/design/perf.md) and
[repository tooling](../../tools/README.md).
