#!/usr/bin/env bash
# Enforce the hot-read latency and allocation budgets without rounding.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
MAX_NS_PER_OP="${MAX_NS_PER_OP:-5}"
MAX_ALLOCS="${MAX_ALLOCS:-0}"
BENCH_TIME="${BENCH_TIME:-1s}"
if ! out="$(go test -run '^$' -bench '^BenchmarkGet$' -benchmem -benchtime="$BENCH_TIME" . 2>&1)"; then
  printf '%s\n' "$out" >&2
  exit 1
fi
printf '%s\n' "$out"
printf '%s\n' "$out" | awk -v max_ns="$MAX_NS_PER_OP" -v max_allocs="$MAX_ALLOCS" '
  /ns\/op/ {
    for (i = 2; i <= NF; i++) {
      if ($i == "ns/op") ns = $(i-1)
      if ($i == "allocs/op") allocs = $(i-1)
    }
  }
  END {
    if (ns !~ /^[0-9]+([.][0-9]+)?$/ || allocs !~ /^[0-9]+$/) {
      print "bench-guard: missing or invalid benchmark metrics"; exit 1
    }
    if (ns > max_ns + 0 || allocs > max_allocs + 0) {
      printf "bench-guard: REGRESSION ns/op=%s (limit=%s), allocs=%s (limit=%s)\n", ns, max_ns, allocs, max_allocs
      exit 1
    }
    printf "bench-guard: OK ns/op=%s allocs=%s\n", ns, allocs
  }'
