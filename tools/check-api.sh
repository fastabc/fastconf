#!/usr/bin/env bash
# Check each module as a consumer using the upstream gorelease implementation.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${VERSION:-v1.1.0}"
# Set BASE=none explicitly only when establishing the first stable API.
BASE="${BASE:-latest}"
if [[ "$BASE" != none && "$BASE" != latest ]]; then
  git -C "$ROOT" rev-parse --verify "refs/tags/$BASE^{commit}" >/dev/null
fi
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
GOBIN="$TMP" GOWORK=off go install golang.org/x/exp/cmd/gorelease@v0.0.0-20260908205506-85c1c2202aba
FAIL=0
while IFS= read -r module; do
  module_base="$BASE"
  manifest="$module/go.mod"; [[ "$module" != . ]] || manifest=go.mod
  if [[ "$BASE" != none && "$BASE" != latest ]] && ! git -C "$ROOT" cat-file -e "$BASE:$manifest" 2>/dev/null; then
    # No manifest in the verified baseline tree means this is the module's
    # first release. Existing modules still require the published baseline.
    module_base=none
  fi
  echo "gorelease: $module (base=$module_base, version=$VERSION)"
  (cd "$ROOT/$module" && GOWORK=off "$TMP/gorelease" "-base=$module_base" "-version=$VERSION") || FAIL=1
done < <(bash "$ROOT/tools/modules.sh" list)
exit "$FAIL"
