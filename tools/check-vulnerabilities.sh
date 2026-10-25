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
  out="$TMP/govulncheck-$$.out"
  if ! (cd "$ROOT/$module" && "$TMP/govulncheck" -show verbose ./...) 2>&1 | tee "$out"; then
    FAIL=1
    if grep -q "from the Go standard library" "$out"; then
      echo "check-vulnerabilities: note: vulnerability reported in Go standard library; ensure Go toolchain is updated to latest patch release" >&2
    fi
  fi
  rm -f "$out"
done <<< "$INVENTORY"
exit "$FAIL"
