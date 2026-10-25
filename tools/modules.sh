#!/usr/bin/env bash
# Discover modules from go.mod; no second inventory or personal workspace.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
COMMAND="${1:-list}"
[[ $# == 0 ]] || shift
MODULES=()
while IFS= read -r file; do
  # Benchmark modules need lint/tidy, but never enter the release inventory.
  if [[ "$file" == benchmarks/* && "$COMMAND" != tidy && "$COMMAND" != tidy-check && "$COMMAND" != lint ]]; then
    continue
  fi
  [[ -f "$file" ]] || continue
  module="${file%/go.mod}"
  [[ "$file" != go.mod ]] || module=.
  MODULES+=("$module")
done < <(git ls-files --cached --others --exclude-standard -- 'go.mod' '**/go.mod' | LC_ALL=C sort -u)
case "$COMMAND" in
  list) printf '%s\n' "${MODULES[@]}"; exit ;;
  matrix)
    printf '{"include":['
    sep=""
    for module in "${MODULES[@]}"; do
      [[ "$module" != . ]] || continue
      floor="$(awk '$1 == "go" {print $2; exit}' "$module/go.mod")"
      printf '%s{"module":"%s","go":"%s"}' "$sep" "$module" "$floor"
      sep=,
    done
    echo ']}'; exit ;;
  test|workspace|tidy|tidy-check|lint) ;;
  exec) [[ $# -gt 0 ]] || { echo "exec requires a command" >&2; exit 2; } ;;
  *) echo "usage: $0 list|matrix|test|workspace|tidy|tidy-check [module ...]|lint [module ...] -- command ...|exec command ..." >&2; exit 2 ;;
esac
SELECTED=("${MODULES[@]}")
LINTER=()
if [[ "$COMMAND" == lint ]]; then
  requested=()
  while [[ $# -gt 0 && "$1" != -- ]]; do requested+=("$1"); shift; done
  [[ $# -gt 1 ]] || { echo "lint requires -- followed by a command" >&2; exit 2; }
  shift
  LINTER=("$@")
  if [[ ${#requested[@]} -gt 0 ]]; then SELECTED=("${requested[@]}"); fi
elif [[ "$COMMAND" != exec && $# -gt 0 ]]; then
  SELECTED=("$@")
fi
for module in "${SELECTED[@]}"; do
  if ! printf '%s\n' "${MODULES[@]}" | grep -Fxq -- "$module"; then
    echo "unknown module: $module" >&2; exit 2
  fi
done
export GOWORK=off
if [[ "$COMMAND" == tidy || "$COMMAND" == tidy-check ]]; then
  exec bash "$ROOT/tools/check-candidate.sh" "--$COMMAND" "${SELECTED[@]}"
fi
if [[ "$COMMAND" == workspace || "$COMMAND" == exec ]]; then
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  PATHS=()
  for module in "${MODULES[@]}"; do PATHS+=("$ROOT/$module"); done
  (cd "$TMP" && go work init "${PATHS[@]}")
  export GOWORK="$TMP/go.work"
  MODFILES=()
  for module in "${MODULES[@]}"; do MODFILES+=("$ROOT/$module/go.mod"); done
  REPLACES=()
  # Read every manifest once, then resolve internal requirements after all
  # module paths are known. Version-qualified replacements preserve MVS.
  while IFS= read -r replacement; do
    REPLACES+=("-replace=$replacement")
  done < <(awk '
    FNR == 1 { in_require = 0 }
    $1 == "module" { dir = FILENAME; sub(/\/go.mod$/, "", dir); dirs[$2] = dir }
    $1 == "require" && $2 == "(" { in_require = 1; next }
    $1 == ")" { in_require = 0 }
    $1 == "require" && $2 != "(" { requires[$2 "@" $3] = $2 }
    in_require && $2 ~ /^v[0-9]/ { requires[$1 "@" $2] = $1 }
    END { for (req in requires) if (requires[req] in dirs) print req "=" dirs[requires[req]] }
  ' "${MODFILES[@]}" | LC_ALL=C sort)
  if [[ ${#REPLACES[@]} -gt 0 ]]; then go work edit "${REPLACES[@]}"; fi
fi
if [[ "$COMMAND" == exec ]]; then
  "$@"
  exit
fi
FAIL=0
for module in "${SELECTED[@]}"; do
  echo "modules: $COMMAND / $module"
  if [[ "$COMMAND" == lint ]]; then
    (cd "$ROOT/$module" && "${LINTER[@]}" run --config "$ROOT/.golangci.yml" --timeout=5m ./...) || FAIL=1
  else
    (cd "$ROOT/$module" && go test -mod=readonly -race -count=1 -timeout=5m ./...) || FAIL=1
  fi
done
exit "$FAIL"
