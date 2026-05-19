#!/usr/bin/env bash
# tools/total-loc-budget.sh — Whole-tree LOC budget guard.
#
# Counts non-test Go lines that belong to the **main module**, i.e.
# every file under the repo root *except* directories owned by a
# nested go.mod sub-module. The find walk explicitly skips .git,
# .worktrees, .idea, and any directory containing a go.mod at depth
# ≥ 2 (each one is independently CI-tested in its own working
# directory). Pairs with tools/loc-budget.sh which guards just the
# fastconf/ main package.
#
# Usage:
#   bash tools/total-loc-budget.sh              # uses default budget
#   MAX_TOTAL_LOC=7100 bash tools/total-loc-budget.sh

set -euo pipefail

# Baseline ticks up with each absorbed P1/P2 batch so the guard keeps
# blocking silent growth without serially rejecting deliberate work.
#   v0.13.0 baseline: 11525
#   v0.15.0 P1+P2 absorb (ctx propagation, DiffReporter backpressure,
#     nil-safety, secret-aware MarshalYAML, isolated provider clients): 11964
#   v0.15.0 T1..T6 absorb (queue-depth telemetry, manager-local registry,
#     deferred WithProviderByName resolution): 12146
#   v0.17 absorb (env/labels/CLI/k8s semantics; pkg/cliadapter,
#     pkg/provider/labels.go + routing_labels.go, multi-axis overlay
#     resolution, sub-module merges): 14874.
#   v0.18.0 H5 absorb (SPEC-A2 Dump replaces MarshalYAML, SPEC-A5
#     YAML-only tag warner, SPEC-A9 MustNew + docstring expansion on
#     WithCodecBridge / MustNew / New): 15188.
#   v0.18.0 pre-release polish absorb (internal/testutil/tracer.go
#     consolidating duplicated recordingTracer/recordingSpan across
#     manager + otel tests; Go 1.22 watcher backport):  ~15520.
# Set to 16200 to allow ~680 LOC of headroom for post-release patches
# while still blocking silent growth.
MAX_TOTAL_LOC="${MAX_TOTAL_LOC:-16200}"

# Discover every nested sub-module (go.mod at depth ≥ 2) and convert
# them into find-friendly prune predicates. Built dynamically so adding
# a new sub-module never silently re-inflates the LOC count.
EXCLUDES=()
while IFS= read -r modfile; do
  d="${modfile%/go.mod}"
  EXCLUDES+=( ! -path "${d}/*" )
done < <(find . -mindepth 2 -name go.mod \
  -not -path "./.git/*" \
  -not -path "./.worktrees/*" \
  -not -path "./.idea/*")

LIVE_LOC=$(
  find . -type f -name "*.go" ! -name "*_test.go" \
    ! -path "./.git/*" \
    ! -path "./.worktrees/*" \
    ! -path "./.idea/*" \
    "${EXCLUDES[@]}" \
    -exec wc -l {} + 2>/dev/null \
    | awk '/total$/{print $1}' \
    | tail -1
)
if [ -z "$LIVE_LOC" ]; then
  LIVE_LOC=$(find . -type f -name "*.go" ! -name "*_test.go" \
    ! -path "./.git/*" ! -path "./.worktrees/*" ! -path "./.idea/*" \
    "${EXCLUDES[@]}" \
    -exec cat {} + | wc -l)
fi

echo "Main-module non-test LOC: ${LIVE_LOC}  (budget: ${MAX_TOTAL_LOC})"
if [ "${LIVE_LOC}" -gt "${MAX_TOTAL_LOC}" ]; then
  echo "ERROR: main-module LOC budget exceeded (${LIVE_LOC} > ${MAX_TOTAL_LOC})" >&2
  echo "  Sub-modules are excluded — see tools/total-loc-budget.sh comment." >&2
  echo "  Review docs/plans/2026-05-14-phase-87-wave5-aggressive-refactor.md SPEC-88." >&2
  exit 1
fi
echo "OK."
