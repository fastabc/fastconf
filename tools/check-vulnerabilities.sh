#!/usr/bin/env bash
# Scan each module against its declared dependencies, without a local workspace.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INVENTORY="$(bash "$ROOT/tools/modules.sh" list)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
GOBIN="$TMP" go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
"$TMP/govulncheck" -version

FAIL=0
while IFS= read -r module; do
  echo "check-vulnerabilities: independent / $module"
  if ! (cd "$ROOT/$module" && "$TMP/govulncheck" -show verbose ./...); then
    FAIL=1
  fi
done <<< "$INVENTORY"
exit "$FAIL"
