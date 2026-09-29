#!/usr/bin/env bash
# Collect repeatable reload benchmarks and CPU/heap profiles in an output directory.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 OUTPUT_DIRECTORY" >&2
  exit 2
fi
mkdir -p "$1"
OUT="$(cd "$1" && pwd)"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cd "$ROOT"
export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
export GOMAXPROCS="${GOMAXPROCS:-4}"

{
  date -u '+%Y-%m-%dT%H:%M:%SZ'
  go version
  go env GOOS GOARCH GOFLAGS
  echo "GOMAXPROCS=$GOMAXPROCS; race=false; sample count=5; benchtime=1s; profile time=3s"
  git rev-parse HEAD
  git status --short
} > "$OUT/environment.txt"
git diff --binary --unified=0 HEAD -- '*.go' > "$OUT/source.diff"
go test -c -o "$TMP/fastconf.test" .
"$TMP/fastconf.test" -test.run '^$' \
  -test.bench '^(BenchmarkGetWarmState|BenchmarkReloadNoop|BenchmarkReloadCommitSmall|BenchmarkReloadLarge|BenchmarkReloadDocument)$' \
  -test.benchmem -test.count=5 -test.benchtime=1s > "$OUT/benchmarks.txt"

for scenario in noop commit large doc4 doc64 doc1024; do
  case "$scenario" in
    noop) bench='^BenchmarkReloadNoop$' ;;
    commit) bench='^BenchmarkReloadCommitSmall$' ;;
    large) bench='^BenchmarkReloadLarge$' ;;
    doc4) bench='^BenchmarkReloadDocument$/^4KiB$' ;;
    doc64) bench='^BenchmarkReloadDocument$/^64KiB$' ;;
    doc1024) bench='^BenchmarkReloadDocument$/^1024KiB$' ;;
  esac
  "$TMP/fastconf.test" -test.run '^$' -test.bench "$bench" -test.benchtime=3s \
    -test.cpuprofile "$OUT/$scenario.cpu.pprof" -test.memprofile "$OUT/$scenario.heap.pprof" \
    > "$OUT/$scenario.profile-benchmark.txt"
  go tool pprof -top -nodecount=20 "$OUT/$scenario.cpu.pprof" > "$OUT/$scenario.cpu.txt"
  go tool pprof -top -cum -nodecount=20 "$OUT/$scenario.cpu.pprof" > "$OUT/$scenario.cpu-cumulative.txt"
  go tool pprof -top -alloc_space -nodecount=20 "$OUT/$scenario.heap.pprof" > "$OUT/$scenario.alloc.txt"
  go tool pprof -top -inuse_space -nodecount=20 "$OUT/$scenario.heap.pprof" > "$OUT/$scenario.inuse.txt"
done
echo "Reload profiles written to $OUT"
