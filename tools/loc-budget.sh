#!/usr/bin/env bash
# tools/loc-budget.sh — LOC budget guard for the fastconf main package.
#
# Counts non-test Go lines in fastconf/ (direct files only, no sub-modules)
# and exits 1 when the count exceeds the current release budget.
#
# Usage:
#   bash tools/loc-budget.sh            # uses MAX_LOC default
#   MAX_LOC=3700 bash tools/loc-budget.sh
#
# Wired into .github/workflows/ci.yml after bench-guard.sh.

set -euo pipefail

# Keep enough headroom for maintenance patches while still blocking quiet
# growth in the root facade.
MAX_LOC="${MAX_LOC:-2800}"

# Count non-test .go files in the repo root only; implementation packages and
# sub-modules are excluded by maxdepth 1.
LIVE_LOC=$(find . -maxdepth 1 -name "*.go" ! -name "*_test.go" \
  -exec wc -l {} + 2>/dev/null \
  | awk '/total$/{print $1}')

# Handle the edge case where only one file exists (no "total" line from wc).
if [ -z "$LIVE_LOC" ]; then
  LIVE_LOC=$(find . -maxdepth 1 -name "*.go" ! -name "*_test.go" \
    -exec cat {} + | wc -l)
fi

echo "Main package (repo root) live LOC: ${LIVE_LOC}  (budget: ${MAX_LOC})"

if [ "${LIVE_LOC}" -gt "${MAX_LOC}" ]; then
  echo "ERROR: LOC budget exceeded (${LIVE_LOC} > ${MAX_LOC})." >&2
  echo "  Move implementation detail out of the root facade or raise MAX_LOC deliberately." >&2
  exit 1
fi

echo "OK: LOC budget respected."
