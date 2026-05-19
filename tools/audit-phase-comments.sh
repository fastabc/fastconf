#!/usr/bin/env bash
# Audit "Phase NN" archaeology comments across the whole tree.
#
# A single Phase pointer table in fastconf/doc.go is the canonical
# anchor; everything else should be folded away. The exit code is
# advisory by default (use --strict to fail when more than MAX leak).
#
# Usage:
#   bash tools/audit-phase-comments.sh            # lists + advisory
#   bash tools/audit-phase-comments.sh --strict   # exit 1 when >MAX
#   MAX_PHASE_COMMENTS=0 bash tools/audit-phase-comments.sh --strict

set -euo pipefail

MAX="${MAX_PHASE_COMMENTS:-0}"
STRICT=0
if [ "${1:-}" = "--strict" ]; then
  STRICT=1
fi

# grep the whole tree, excluding archive/worktree/git noise.
# Pattern covers "Phase N" anywhere in a comment line, not just at the start.
MATCHES=$(grep -rn "Phase [0-9]" --include='*.go' . \
  --exclude-dir='.git' \
  --exclude-dir='.worktrees' \
  --exclude-dir='archive' 2>/dev/null \
  | grep -v '^\./doc.go:' \
  | grep -v 'Phase-pointer table' \
  || true)

if [ -z "$MATCHES" ]; then
  echo "Phase comment archaeology: 0 (budget: ${MAX}). OK."
  exit 0
fi

COUNT=$(printf '%s\n' "$MATCHES" | wc -l | tr -d ' ')
echo "Phase comment references (excluding doc.go anchor): ${COUNT}  (budget: ${MAX})"
printf '%s\n' "$MATCHES"

if [ "$STRICT" -eq 1 ] && [ "${COUNT}" -gt "${MAX}" ]; then
  echo "ERROR: phase comment archaeology exceeded." >&2
  exit 1
fi
