#!/usr/bin/env bash
# tools/loc-budget.sh — LOC budget guard for the fastconf main package.
#
# Counts non-test Go lines in fastconf/ (direct files only, no sub-modules)
# and exits 1 when the count exceeds the Wave 5 ratchet.
#
# Usage:
#   bash tools/loc-budget.sh            # uses MAX_LOC default
#   MAX_LOC=3700 bash tools/loc-budget.sh
#
# Wired into .github/workflows/ci.yml after bench-guard.sh.

set -euo pipefail

# Baseline ticks up with each absorbed feature batch so the guard keeps
# blocking silent growth without serially rejecting deliberate work.
#   v0.13.0 baseline: 5111
#   v0.15.0 P1+P2 absorb: 5205
#   v0.15.0 T1..T6 absorb (queue-depth telemetry + manager-local registry): 5387
# Keep ~100 LOC of maintenance headroom while preventing quiet growth.
MAX_LOC="${MAX_LOC:-5487}"

# Count non-test .go files in the repo root only (v0.9.0 flatten: main API
# files live at the repo root; sub-package directories like bus/, otel/,
# pkg/, policy/, providers/, validate/, render/, metrics/, contracts/ are
# excluded by maxdepth 1).
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
  echo "  Review docs/plans/2026-05-14-phase-87-wave5-aggressive-refactor.md SPEC-88." >&2
  exit 1
fi

echo "OK: LOC budget respected."
