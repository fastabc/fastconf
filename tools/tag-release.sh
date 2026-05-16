#!/usr/bin/env bash
# tag-release.sh — create (and optionally push) release tags for all FastConf modules.
#
# Usage:
#   ./tools/tag-release.sh <version> [--push] [--force]
#
# Examples:
#   ./tools/tag-release.sh v0.8.1                    # create tags locally (skip existing)
#   ./tools/tag-release.sh v0.8.1 --push             # create + push to origin
#   ./tools/tag-release.sh v0.8.1 --force            # delete & recreate existing local tags
#   ./tools/tag-release.sh v0.8.1 --force --push     # delete remote + local, then recreate & push
#
# Flags:
#   --push    Push newly created tags to origin.
#   --force   Delete existing local tags before recreating (alias: --retag).
#             When combined with --push, also deletes the remote tags before pushing.
#
# The script honours the Go multi-module tag convention:
#   root module      → vX.Y.Z
#   sub-module at a/ → a/vX.Y.Z
#
# All sub-modules listed in RELEASING.md are tagged in a single run.

set -euo pipefail

VERSION="${1:-}"
PUSH=false
FORCE=false

if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 <version> [--push] [--force|--retag]" >&2
  exit 1
fi

# Accept either "0.8.1" or "v0.8.1".
if [[ "$VERSION" != v* ]]; then
  VERSION="v${VERSION}"
fi

for arg in "${@:2}"; do
  case "$arg" in
    --push) PUSH=true ;;
    --force|--retag) FORCE=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Verify we are on the expected git state.
if ! git diff --quiet HEAD; then
  echo "error: working tree has uncommitted changes — commit or stash first." >&2
  exit 1
fi

# -------------------------------------------------------------------------
# Module tag matrix (path prefix → human name)
# Each entry is "tag_prefix:human_name". Root module uses an empty prefix.
# cmd/fastconfctl and cmd/fastconfgen are part of the root module (no separate tag).
# -------------------------------------------------------------------------
declare -a MODULES=(
  ":fastconf (root)"
  "observability/metrics/prometheus:observability/metrics/prometheus"
  "observability/otel:observability/otel"
  "policy/cue:policy/cue"
  "policy/opa:policy/opa"
  "validate/cue/cuelang:validate/cue/cuelang"
  "validate/playground:validate/playground"
)

TAGS_CREATED=()
TAGS_SKIPPED=()
TAGS_RETAGGED=()
TAGS_REMOTE_DELETED=()

for entry in "${MODULES[@]}"; do
  prefix="${entry%%:*}"
  name="${entry#*:}"

  if [[ -z "$prefix" ]]; then
    tag="$VERSION"
  else
    tag="${prefix}/${VERSION}"
  fi

  if git rev-parse "$tag" >/dev/null 2>&1; then
    if [[ "$FORCE" == true ]]; then
      # Delete remote tag first if we will be pushing (to avoid push rejection).
      if [[ "$PUSH" == true ]]; then
        if git ls-remote --tags origin "refs/tags/${tag}" | grep -q .; then
          git push origin ":refs/tags/${tag}"
          echo "  del   $tag  (remote)"
          TAGS_REMOTE_DELETED+=("$tag")
        fi
      fi
      git tag -d "$tag"
      git tag -a "$tag" -m "${name} ${VERSION}"
      echo "  retag $tag"
      TAGS_RETAGGED+=("$tag")
      TAGS_CREATED+=("$tag")
    else
      echo "  skip  $tag  (already exists)"
      TAGS_SKIPPED+=("$tag")
    fi
    continue
  fi

  git tag -a "$tag" -m "${name} ${VERSION}"
  echo "  tag   $tag"
  TAGS_CREATED+=("$tag")
done

echo ""
echo "Created ${#TAGS_CREATED[@]} tag(s) (${#TAGS_RETAGGED[@]} retagged), skipped ${#TAGS_SKIPPED[@]} existing."

if [[ "$PUSH" == true ]]; then
  if [[ ${#TAGS_CREATED[@]} -gt 0 ]]; then
    echo "Pushing tags to origin..."
    git push origin "${TAGS_CREATED[@]}"
    echo "Done."
  else
    echo "Nothing new to push."
  fi
else
  if [[ ${#TAGS_CREATED[@]} -gt 0 ]]; then
    echo ""
    echo "To push tags:"
    echo "  git push origin ${TAGS_CREATED[*]}"
  fi
fi
