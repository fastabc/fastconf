#!/usr/bin/env bash
# tag-release.sh — create, retag, or delete release tags for FastConf modules.
#
# Default behaviour is CHANGED-ONLY: the root module is always tagged
# (its tag drives the binary release workflow); a satellite module is
# tagged only when its directory content changed since that module's own
# most recent release tag. Use --all to tag every module unconditionally.
#
# The module list is derived from go.work (single source of truth,
# guarded by tools/check-module-matrix.sh). Root module → vX.Y.Z,
# sub-module at a/b → a/b/vX.Y.Z (Go multi-module tag convention).
#
# Usage:
#   ./tools/tag-release.sh <version> [--push] [--force|--retag] [--delete] [--all] [--dry-run]
set -euo pipefail

VERSION="${1:-}"
PUSH=false; FORCE=false; DELETE=false; ALL=false; DRYRUN=false

if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 <version> [--push] [--force|--retag] [--delete] [--all] [--dry-run]" >&2
  exit 1
fi
[[ "$VERSION" != v* ]] && VERSION="v${VERSION}"

for arg in "${@:2}"; do
  case "$arg" in
    --push) PUSH=true ;;
    --force|--retag) FORCE=true ;;
    --delete) DELETE=true ;;
    --all) ALL=true ;;
    --dry-run) DRYRUN=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done
if [[ "$DELETE" == true && "$FORCE" == true ]]; then
  echo "error: --delete cannot be combined with --force/--retag." >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! git diff --quiet HEAD; then
  echo "error: working tree has uncommitted changes — commit or stash first." >&2
  exit 1
fi

# Module prefixes from go.work; "" denotes the root module.
PREFIXES=("")
while IFS= read -r m; do
  PREFIXES+=("$m")
done < <(awk '
  $1 == "use" && $2 == "(" { in_use = 1; next }
  in_use && $1 == ")" { in_use = 0; next }
  in_use { sub(/^\.\//, "", $1); if ($1 != ".") print $1 }
' go.work | sort)

last_tag_for() { # $1 = prefix ("" = root)
  if [[ -z "$1" ]]; then
    git tag -l 'v[0-9]*' --sort=-v:refname | head -n1
  else
    git tag -l "$1/v[0-9]*" --sort=-v:refname | head -n1
  fi
}

changed_since() { # $1 = dir, $2 = last tag; true when changed or never tagged
  [[ -z "$2" ]] && return 0
  ! git diff --quiet "$2..HEAD" -- "$1"
}

remote_tag_exists() {
  local refs
  if ! refs="$(git ls-remote --tags origin "refs/tags/$1")"; then
    echo "error: failed to inspect remote tag $1 on origin." >&2
    exit 1
  fi
  [[ -n "$refs" ]]
}

TAGS_CREATED=(); TAGS_SKIPPED=(); TAGS_RETAGGED=()
TAGS_DELETED=(); TAGS_REMOTE_DELETED=(); TAGS_UNCHANGED=()

for prefix in "${PREFIXES[@]}"; do
  if [[ -z "$prefix" ]]; then tag="$VERSION"; dir="."; name="fastconf (root)"
  else tag="${prefix}/${VERSION}"; dir="$prefix"; name="$prefix"; fi

  if [[ "$DELETE" == true ]]; then
    if git rev-parse "$tag" >/dev/null 2>&1; then
      $DRYRUN || git tag -d "$tag"
      echo "  del   $tag  (local)"; TAGS_DELETED+=("$tag")
    else
      echo "  skip  $tag  (missing local)"; TAGS_SKIPPED+=("$tag")
    fi
    if [[ "$PUSH" == true ]] && remote_tag_exists "$tag"; then
      $DRYRUN || git push origin ":refs/tags/${tag}"
      echo "  del   $tag  (remote)"; TAGS_REMOTE_DELETED+=("$tag")
    fi
    continue
  fi

  # Changed-only gate (satellites only; root always releases).
  if [[ -n "$prefix" && "$ALL" != true ]]; then
    last="$(last_tag_for "$prefix")"
    if ! changed_since "$dir" "$last"; then
      echo "  skip  $tag  (unchanged since ${last})"
      TAGS_UNCHANGED+=("$tag")
      continue
    fi
  fi

  # Guard: a satellite must not ship a v0.0.0 placeholder root require.
  if [[ -n "$prefix" ]] && grep -Eq 'github\.com/fastabc/fastconf v0\.0\.0' "$dir/go.mod"; then
    echo "error: $dir/go.mod pins fastconf at a v0.0.0 placeholder;" >&2
    echo "       run the satellite-require bump first (see RELEASING.md)." >&2
    exit 1
  fi

  if git rev-parse "$tag" >/dev/null 2>&1; then
    if [[ "$FORCE" == true ]]; then
      if [[ "$PUSH" == true ]] && remote_tag_exists "$tag"; then
        $DRYRUN || git push origin ":refs/tags/${tag}"
        echo "  del   $tag  (remote)"; TAGS_REMOTE_DELETED+=("$tag")
      fi
      $DRYRUN || { git tag -d "$tag"; git tag -a "$tag" -m "${name} ${VERSION}"; }
      echo "  retag $tag"; TAGS_RETAGGED+=("$tag"); TAGS_CREATED+=("$tag")
    else
      echo "  skip  $tag  (already exists)"; TAGS_SKIPPED+=("$tag")
    fi
    continue
  fi

  $DRYRUN || git tag -a "$tag" -m "${name} ${VERSION}"
  echo "  tag   $tag"; TAGS_CREATED+=("$tag")
done

if [[ "$DELETE" == true ]]; then
  echo ""
  echo "Deleted ${#TAGS_DELETED[@]} local tag(s), ${#TAGS_REMOTE_DELETED[@]} remote tag(s); skipped ${#TAGS_SKIPPED[@]}."
  exit 0
fi

echo ""
echo "Created ${#TAGS_CREATED[@]} tag(s) (${#TAGS_RETAGGED[@]} retagged), skipped ${#TAGS_SKIPPED[@]} existing, ${#TAGS_UNCHANGED[@]} unchanged."
$DRYRUN && { echo "(dry-run: no tags were written)"; exit 0; }

if [[ "$PUSH" == true ]]; then
  if [[ ${#TAGS_CREATED[@]} -gt 0 ]]; then
    echo "Pushing tags to origin..."
    git push origin "${TAGS_CREATED[@]}"
    echo "Done."
  else
    echo "Nothing new to push."
  fi
elif [[ ${#TAGS_CREATED[@]} -gt 0 ]]; then
  echo ""
  echo "To push tags:"
  echo "  git push origin ${TAGS_CREATED[*]}"
fi
