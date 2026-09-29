#!/usr/bin/env bash
# Check boundary values and preserve failed Go command diagnostics.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/go" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$BENCH_OUTPUT"
exit "${BENCH_EXIT:-0}"
MOCK
chmod +x "$TMP/go"
export PATH="$TMP:$PATH"
export MAX_NS_PER_OP=5 MAX_ALLOCS=0
export BENCH_OUTPUT='BenchmarkGet-8 100 5.00 ns/op 0 B/op 0 allocs/op'
bash "$ROOT/tools/bench-guard.sh" > "$TMP/output"
for BENCH_OUTPUT in \
  'BenchmarkGet-8 100 5.01 ns/op 0 B/op 0 allocs/op' \
  'BenchmarkGet-8 100 4.00 ns/op 8 B/op 1 allocs/op' \
  'PASS'; do
  export BENCH_OUTPUT
  if bash "$ROOT/tools/bench-guard.sh" > "$TMP/output" 2>&1; then
    echo "unexpected success: $BENCH_OUTPUT" >&2; exit 1
  fi
done
export BENCH_OUTPUT='compiler failed' BENCH_EXIT=1
if bash "$ROOT/tools/bench-guard.sh" > "$TMP/output" 2>&1; then exit 1; fi
grep -q 'compiler failed' "$TMP/output"
echo 'bench-guard tests: OK'
