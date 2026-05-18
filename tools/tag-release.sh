#!/usr/bin/env bash
# tag-release.sh — create, retag, or delete release tags for all FastConf modules.
#
# Usage:
#   ./tools/tag-release.sh <version> [--push] [--force|--retag] [--delete]
#
# Examples:
#   ./tools/tag-release.sh v0.8.1                    # create tags locally (skip existing)
#   ./tools/tag-release.sh v0.8.1 --push             # create + push to origin
#   ./tools/tag-release.sh v0.8.1 --force            # delete & recreate existing local tags
#   ./tools/tag-release.sh v0.8.1 --force --push     # delete remote + local, then recreate & push
#   ./tools/tag-release.sh v0.8.1 --delete           # delete local tags
#   ./tools/tag-release.sh v0.8.1 --delete --push    # delete local + remote tags
#
# Flags:
#   --push    Push newly created tags to origin, or delete matching remote tags with --delete.
#   --force   Delete existing local tags before recreating (alias: --retag).
#             When combined with --push, also deletes the remote tags before pushing.
#   --delete  Delete matching tags instead of creating them.
#             When combined with --push, also deletes the matching remote tags.
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
DELETE=false

if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 <version> [--push] [--force|--retag] [--delete]" >&2
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
    --delete) DELETE=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

if [[ "$DELETE" == true && "$FORCE" == true ]]; then
  echo "error: --delete cannot be combined with --force/--retag." >&2
  exit 1
fi

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
  "cue:cue"
  "integrations/cli/pflag:integrations/cli/pflag"
  "integrations/log/phuslu:integrations/log/phuslu"
  "integrations/log/zerolog:integrations/log/zerolog"
  "observability/metrics/prometheus:observability/metrics/prometheus"
  "observability/otel:observability/otel"
  "policy/opa:policy/opa"
  "providers/s3:providers/s3"
  "validate/playground:validate/playground"
)

TAGS_CREATED=()
TAGS_SKIPPED=()
TAGS_RETAGGED=()
TAGS_DELETED=()
TAGS_REMOTE_DELETED=()

remote_tag_exists() {
  local tag="$1"
  local refs

  if ! refs="$(git ls-remote --tags origin "refs/tags/${tag}")"; then
    echo "error: failed to inspect remote tag ${tag} on origin." >&2
    exit 1
  fi

  [[ -n "$refs" ]]
}

for entry in "${MODULES[@]}"; do
  prefix="${entry%%:*}"
  name="${entry#*:}"

  if [[ -z "$prefix" ]]; then
    tag="$VERSION"
  else
    tag="${prefix}/${VERSION}"
  fi

  if [[ "$DELETE" == true ]]; then
    if git rev-parse "$tag" >/dev/null 2>&1; then
      git tag -d "$tag"
      echo "  del   $tag  (local)"
      TAGS_DELETED+=("$tag")
    else
      echo "  skip  $tag  (missing local)"
      TAGS_SKIPPED+=("$tag")
    fi

    if [[ "$PUSH" == true ]] && remote_tag_exists "$tag"; then
      git push origin ":refs/tags/${tag}"
      echo "  del   $tag  (remote)"
      TAGS_REMOTE_DELETED+=("$tag")
    fi
    continue
  fi

  if git rev-parse "$tag" >/dev/null 2>&1; then
    if [[ "$FORCE" == true ]]; then
      # Delete remote tag first if we will be pushing (to avoid push rejection).
      if [[ "$PUSH" == true ]]; then
        if remote_tag_exists "$tag"; then
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

if [[ "$DELETE" == true ]]; then
  echo ""
  echo "Deleted ${#TAGS_DELETED[@]} local tag(s), ${#TAGS_REMOTE_DELETED[@]} remote tag(s); skipped ${#TAGS_SKIPPED[@]} missing local."
  exit 0
fi

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
