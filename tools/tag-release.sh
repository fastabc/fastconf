#!/usr/bin/env bash
# Release every discovered module at one version. Published tags are immutable.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VERSION="${1:-}"
[[ $# == 0 ]] || shift
PUSH=false; DRYRUN=false
for arg in "$@"; do
  case "$arg" in
    --push) PUSH=true ;;
    --dry-run) DRYRUN=true ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done
if [[ ! "$VERSION" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "usage: $0 vX.Y.Z[-prerelease] [--dry-run] [--push]" >&2; exit 2
fi
if [[ "$VERSION" == *-* ]]; then
  IFS=. read -r -a identifiers <<< "${VERSION#*-}"
  for identifier in "${identifiers[@]}"; do
    if [[ "$identifier" =~ ^0[0-9]+$ ]]; then
      echo "invalid numeric prerelease: $identifier" >&2; exit 2
    fi
  done
fi
if ! $DRYRUN && [[ -n "$(git status --porcelain)" ]]; then
  echo "commit changes before tagging" >&2; exit 1
fi
TAGS=()
MODULE_PATH="$(awk '$1 == "module" {print $2}' go.mod)"
# Validate the entire plan before creating any tag.
while IFS= read -r module; do
  while read -r dep version; do
    if [[ "$dep" == "$MODULE_PATH" || "$dep" == "$MODULE_PATH/"* ]]; then
      if [[ "$version" != "$VERSION" ]]; then
        echo "$module requires $dep $version; align it to $VERSION first" >&2; exit 1
      fi
    fi
  done < <(awk '$1 == "require" {print $2, $3; next} {print $1, $2}' "$module/go.mod")
  prefix="$module/"; [[ "$module" != . ]] || prefix=""
  tag="$prefix$VERSION"
  if git show-ref --verify --quiet "refs/tags/$tag" && [[ "$(git rev-list -n1 "$tag")" != "$(git rev-parse HEAD)" ]]; then
    echo "$tag already points to a different commit; release a new version" >&2; exit 1
  fi
  TAGS+=("$tag")
done < <(bash tools/modules.sh list)
printf '%s\n' "${TAGS[@]}"
$DRYRUN && exit 0
for tag in "${TAGS[@]}"; do
  git show-ref --verify --quiet "refs/tags/$tag" || git tag -a "$tag" -m "fastconf $tag"
done
if $PUSH; then git push --atomic origin "${TAGS[@]}"; fi
