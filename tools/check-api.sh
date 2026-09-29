#!/usr/bin/env bash
# Check each module as a consumer using the upstream gorelease implementation.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${VERSION:-v1.0.0}"
# Set BASE=none explicitly only when establishing the first stable API.
BASE="${BASE:-latest}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
GOBIN="$TMP" GOWORK=off go install golang.org/x/exp/cmd/gorelease@v0.0.0-20260908205506-85c1c2202aba
FAIL=0
while IFS= read -r module; do
  echo "gorelease: $module (base=$BASE, version=$VERSION)"
  (cd "$ROOT/$module" && GOWORK=off "$TMP/gorelease" "-base=$BASE" "-version=$VERSION") || FAIL=1
done < <(bash "$ROOT/tools/modules.sh" list)
exit "$FAIL"
