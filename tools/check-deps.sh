#!/usr/bin/env bash
# check-deps.sh — enforce the pkg/* inter-dependency whitelist.
# Fails if any pkg package imports another pkg package outside the allowed edges.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FAIL=0

# Allowed edges: "importer:importee"
# pkg/mappath and pkg/typed are leaf utils — any pkg/* may depend on them.
ALLOWED=(
  "pkg/discovery:pkg/profile"
  "pkg/generator:pkg/mappath"
  "pkg/provider:pkg/decoder"
  "pkg/provider:pkg/mappath"
  "pkg/provider:pkg/typed"
  "pkg/mappath:pkg/typed"
  "pkg/transform:pkg/mappath"
  "pkg/parser:pkg/decoder"
)

is_allowed() {
  local importer="$1" importee="$2"
  for edge in "${ALLOWED[@]}"; do
    local lhs="${edge%%:*}" rhs="${edge##*:}"
    if [[ "$importer" == *"$lhs" && "$importee" == *"$rhs" ]]; then
      return 0
    fi
  done
  return 1
}

# Walk every pkg/* package and check its imports
while IFS= read -r pkg; do
  # Get direct imports that live under pkg/
  while IFS= read -r dep; do
    [[ "$dep" == *"github.com/fastabc/fastconf/pkg/"* ]] || continue
    if ! is_allowed "$pkg" "$dep"; then
      echo "FORBIDDEN DEP: $pkg → $dep" >&2
      FAIL=1
    fi
  done < <(cd "$ROOT" && go list -f '{{ range .Imports }}{{ . }}{{ "\n" }}{{ end }}' "$pkg" 2>/dev/null)
done < <(cd "$ROOT" && go list ./pkg/... 2>/dev/null)

if [ "$FAIL" -ne 0 ]; then
  echo "check-deps: FAILED — forbidden inter-pkg dependencies found" >&2
  exit 1
fi
echo "check-deps: OK"
