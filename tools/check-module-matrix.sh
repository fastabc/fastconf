#!/usr/bin/env bash
# check-module-matrix.sh — keep go.work, Makefile test-all, and CI's
# independent-module matrix in lockstep.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

work_modules() {
  awk '
    $1 == "use" && $2 == "(" { in_use = 1; next }
    in_use && $1 == ")" { in_use = 0; next }
    in_use {
      gsub(/"/, "", $1)
      sub(/^\.\//, "", $1)
      if ($1 != ".") print $1
    }
    $1 == "use" && $2 != "(" {
      gsub(/"/, "", $2)
      sub(/^\.\//, "", $2)
      if ($2 != ".") print $2
    }
  ' "$ROOT/go.work" | sort
}

ci_modules() {
  awk '
    /^[[:space:]]+module:$/ { in_module = 1; next }
    in_module && /^[[:space:]]+- / {
      sub(/^[[:space:]]+- /, "", $0)
      print $0
      next
    }
    in_module && /^[[:space:]]*[[:alpha:]_-]+:/ { in_module = 0 }
  ' "$ROOT/.github/workflows/ci.yml" | sort
}

make_modules() {
  sed -n '/^test-all:/,/^[^[:space:]]/p' "$ROOT/Makefile" \
    | sed -n 's/^[[:space:]]*cd[[:space:]]\{1,\}\([^[:space:]]*\)[[:space:]]\{1,\}&&[[:space:]]\{1,\}go test.*/\1/p' \
    | sort
}

work_modules > "$TMP/go.work"
ci_modules > "$TMP/ci"
make_modules > "$TMP/make"

FAIL=0
if ! diff -u "$TMP/go.work" "$TMP/ci"; then
  echo "check-module-matrix: CI module matrix does not match go.work" >&2
  FAIL=1
fi
if ! diff -u "$TMP/go.work" "$TMP/make"; then
  echo "check-module-matrix: Makefile test-all does not match go.work" >&2
  FAIL=1
fi

while IFS= read -r module; do
  if [ ! -f "$ROOT/$module/go.mod" ]; then
    echo "check-module-matrix: missing go.mod for $module" >&2
    FAIL=1
  fi
done < "$TMP/go.work"

if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
echo "check-module-matrix: OK"
