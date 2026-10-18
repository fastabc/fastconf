#!/usr/bin/env bash
# Discover modules from go.mod; no second inventory or personal workspace.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
COMMAND="${1:-list}"
[[ $# == 0 ]] || shift
MODULES=()
while IFS= read -r file; do
  # Benchmark modules also need tidy, but never enter the release inventory.
  if [[ "$file" == benchmarks/* && "$COMMAND" != tidy && "$COMMAND" != tidy-check ]]; then
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
  test|workspace|tidy|tidy-check) ;;
  exec) [[ $# -gt 0 ]] || { echo "exec requires a command" >&2; exit 2; } ;;
  *) echo "usage: $0 list|matrix|test|workspace|tidy|tidy-check [module ...]|exec command ..." >&2; exit 2 ;;
esac
SELECTED=("${MODULES[@]}")
if [[ "$COMMAND" != exec && $# -gt 0 ]]; then SELECTED=("$@"); fi
for module in "${SELECTED[@]}"; do
  if ! printf '%s\n' "${MODULES[@]}" | grep -Fxq -- "$module"; then
    echo "unknown module: $module" >&2; exit 2
  fi
done
export GOWORK=off
if [[ "$COMMAND" == tidy || "$COMMAND" == tidy-check ]]; then
  ARGS=(mod tidy)
  [[ "$COMMAND" != tidy-check ]] || ARGS+=(-diff)
  FAIL=0
  for module in "${SELECTED[@]}"; do
    echo "modules: $COMMAND / $module"
    (cd "$ROOT/$module" && go "${ARGS[@]}") || FAIL=1
  done
  if [[ "$FAIL" != 0 && "$COMMAND" == tidy-check ]]; then
    echo "Resolve any errors above, then run make tidy and commit the dependency metadata." >&2
  fi
  exit "$FAIL"
fi
if [[ "$COMMAND" == workspace || "$COMMAND" == exec ]]; then
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  PATHS=()
  for module in "${MODULES[@]}"; do PATHS+=("$ROOT/$module"); done
  (cd "$TMP" && go work init "${PATHS[@]}")
  export GOWORK="$TMP/go.work"
  for dep in "${MODULES[@]}"; do
    path="$(awk '$1 == "module" {print $2}' "$dep/go.mod")"
    for module in "${MODULES[@]}"; do
      req="$(awk -v p="$path" '$1 == p {print $2} $1 == "require" && $2 == p {print $3}' "$module/go.mod")"
      [[ -z "$req" ]] || go work edit "-replace=$path@$req=$ROOT/$dep"
    done
  done
fi
if [[ "$COMMAND" == exec ]]; then
  "$@"
  exit
fi
FAIL=0
for module in "${SELECTED[@]}"; do
  echo "modules: $COMMAND / $module"
  (cd "$ROOT/$module" && go test -mod=readonly -race -count=1 -timeout=5m ./...) || FAIL=1
done
exit "$FAIL"
