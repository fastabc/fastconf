#!/usr/bin/env bash
# loc-budget.sh — LOC budget guard (root facade + whole main module).
#
# Check 1: non-test Go LOC of the repo-root package        (MAX_LOC)
# Check 2: non-test Go LOC of the entire main module,
#          excluding nested go.mod sub-modules             (MAX_TOTAL_LOC)
#
# Usage:
#   bash tools/loc-budget.sh
#   MAX_LOC=2000 MAX_TOTAL_LOC=15500 bash tools/loc-budget.sh
#
# Ratcheted 2026-06-12 after Wave J consolidation (see
# docs/plans/2026-06-12-wave-j-optimization.md): budgets set to the
# measured live values plus a one-feature margin (~300 root, ~500
# main-module) so quiet growth is blocked; raise deliberately in a PR
# when a feature legitimately needs the room.
set -euo pipefail

MAX_LOC="${MAX_LOC:-2000}"
MAX_TOTAL_LOC="${MAX_TOTAL_LOC:-16900}"
FAIL=0

count() { # $@ = find args; prints summed LOC (0 when no files)
  local n
  n=$(find "$@" -exec wc -l {} + 2>/dev/null | awk '/total$/{s=$1} END{print s+0}')
  [ "$n" -gt 0 ] || n=$(find "$@" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
  echo "$n"
}

ROOT_LOC=$(count . -maxdepth 1 -name '*.go' ! -name '*_test.go')
echo "Root facade live LOC: ${ROOT_LOC}  (budget: ${MAX_LOC})"
[ "$ROOT_LOC" -le "$MAX_LOC" ] || { echo "ERROR: root LOC budget exceeded." >&2; FAIL=1; }

EXCLUDES=()
while IFS= read -r modfile; do
  EXCLUDES+=( ! -path "${modfile%/go.mod}/*" )
done < <(find . -mindepth 2 -name go.mod \
  -not -path "./.git/*" -not -path "./.worktrees/*" -not -path "./.idea/*")

TOTAL_LOC=$(count . -type f -name '*.go' ! -name '*_test.go' \
  ! -path './.git/*' ! -path './.worktrees/*' ! -path './.idea/*' "${EXCLUDES[@]}")
echo "Main-module non-test LOC: ${TOTAL_LOC}  (budget: ${MAX_TOTAL_LOC})"
[ "$TOTAL_LOC" -le "$MAX_TOTAL_LOC" ] || { echo "ERROR: main-module LOC budget exceeded." >&2; FAIL=1; }

[ "$FAIL" -eq 0 ] && echo "OK: LOC budgets respected."
exit "$FAIL"
