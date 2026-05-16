#!/usr/bin/env bash
# Phase 36 (v0.5 SPEC-36) — bench regression guard.
#
# Runs BenchmarkGet from package fastconf and asserts:
#   * ns/op  <= MAX_NS_PER_OP   (default 5)
#   * allocs <= MAX_ALLOCS      (default 0)
#
# Wired into CI so any change that regresses the lock-free read path
# fails the build instead of silently shipping. The benchmark lives in
# bench_test.go and is intentionally minimal: a tiny config so the
# measurement reflects only the atomic.Pointer.Load + struct-pointer
# return cost.

set -euo pipefail

MAX_NS_PER_OP=${MAX_NS_PER_OP:-5}
MAX_ALLOCS=${MAX_ALLOCS:-0}
BENCH_TIME=${BENCH_TIME:-1s}

cd "$(dirname "$0")/.."

# -run x  ⇒ skip every Test*; we only want the bench.
out=$(go test -run x -bench '^BenchmarkGet$' -benchmem -benchtime="$BENCH_TIME" . 2>&1)
echo "$out"

# Pick the LAST line containing "ns/op" — Go's testing package may
# split a single Benchmark output line when other goroutines write to
# the same stdout stream (the manager's INFO logger does so during
# New). Matching on "ns/op" is robust to that interleaving.
line=$(echo "$out" | grep -E 'ns/op' | tail -1)
if [[ -z "$line" ]]; then
  echo "bench-guard: no BenchmarkGet output found" >&2
  exit 1
fi

# Format: "BenchmarkGet-8   N   X.XX ns/op   Y B/op   Z allocs/op"
# But interleaved logs may shift columns; use the canonical positions
# of the "ns/op" and "allocs/op" tokens themselves.
ns=$(echo "$line" | awk '{ for(i=1;i<=NF;i++) if($i=="ns/op") print $(i-1) }')
allocs=$(echo "$line" | awk '{ for(i=1;i<=NF;i++) if($i=="allocs/op") print $(i-1) }')

# ns is a float; truncate to integer for shell comparison.
ns_int=${ns%.*}

fail=0
if (( ns_int > MAX_NS_PER_OP )); then
  echo "bench-guard: REGRESSION ns/op=$ns > limit=$MAX_NS_PER_OP" >&2
  fail=1
fi
if (( allocs > MAX_ALLOCS )); then
  echo "bench-guard: REGRESSION allocs=$allocs > limit=$MAX_ALLOCS" >&2
  fail=1
fi

if (( fail == 0 )); then
  echo "bench-guard: OK  ns/op=$ns  allocs=$allocs"
fi
exit $fail
