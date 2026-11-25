#!/usr/bin/env bash
# Validate candidate modules through the module protocol. All synthetic
# archives, downloaded modules and generated sums live in a disposable checkout.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'chmod -R u+w "$TMP"; rm -rf "$TMP"' EXIT
SOURCE_VERSION="${VERSION:-v1.6.0}"
if [[ "${1:-}" == --api ]]; then
  shift
  # Keep the published baseline distinct from local candidate archives. A
  # prerelease of the next patch preserves the major/minor compatibility rules
  # and permits checking VERSION against its own already-published release.
  check_version() {
    python3 - "$1" <<'PY'
import sys
version = sys.argv[1]
if "-" in version:
    print(version + ".api-check")
else:
    prefix, patch = version.rsplit(".", 1)
    print(f"{prefix}.{int(patch) + 1}-api-check")
PY
  }
  # Makefile/CI set API_VERSION explicitly; it must not name a published tag
  # either. Convert it independently because API and dependency targets differ.
  API_VERSION="$(check_version "${API_VERSION:-${VERSION:-v1.7.0}}")"
  VERSION="$(check_version "$SOURCE_VERSION")"
  export VERSION API_VERSION BASE="${BASE:-v1.6.0}"
  [[ $# -gt 0 ]] || set -- bash tools/check-api.sh
else
  VERSION="$SOURCE_VERSION"
fi

python3 "$ROOT/tools/build-candidate-proxy.py" "$ROOT" "$TMP" "$VERSION" "$SOURCE_VERSION"

# Module discovery uses git ls-files; this index belongs only to the copy.
git -C "$TMP/checkout" init -q
git -C "$TMP/checkout" add --all
# Preserve the explicit baseline tree so API checks can distinguish a genuinely
# new module from a published module whose baseline failed to download.
if [[ -n "${BASE:-}" && "$BASE" != none && "$BASE" != latest ]]; then
  git -C "$TMP/checkout" fetch --quiet --no-tags --depth=1 "$ROOT" "refs/tags/$BASE:refs/tags/$BASE"
fi
export GOWORK=off
export GOMODCACHE="$TMP/modcache"
export GOPROXY="file://$TMP/proxy,${GOPROXY:-https://proxy.golang.org,direct}"
export GONOSUMDB=github.com/fastabc/fastconf
export GOPRIVATE=
export GONOPROXY=
echo "check-candidate: $VERSION from checkout; isolated proxy/cache; GOWORK=off"
# Tidy compares with the original metadata before any download preparation
# can fill unrelated sums. Only its explicit write mode updates the source tree.
if [[ "${1:-}" == --tidy || "${1:-}" == --tidy-check ]]; then
  MODE="${1#--}"
  shift
  (cd "$TMP/checkout" && python3 tools/tidy-candidate.py "$ROOT" "$VERSION" "$MODE" "$@")
  exit
fi
# Floor jobs pin an old toolchain; modules above it cannot load there and are
# not under test, so leave their checksums unfilled.
GOVER="$(go env GOVERSION)"; GOVER="${GOVER#go}"
TOOLCHAIN="$(go env GOTOOLCHAIN)"
above_toolchain() {
  python3 - "$1" "$GOVER" <<'PYTHON'
import re
import sys
def version(value):
    parts = re.match(r"(\d+)\.(\d+)(?:\.(\d+))?", value)
    return tuple(int(part or 0) for part in parts.groups())
sys.exit(0 if version(sys.argv[1]) > version(sys.argv[2]) else 1)
PYTHON
}
inventory="$(bash "$TMP/checkout/tools/modules.sh" list)"
while IFS= read -r module; do
  mod="$TMP/checkout/$module/go.mod"
  candidates=()
  while read -r path version; do
    if [[ "$version" == "$VERSION" && ("$path" == github.com/fastabc/fastconf || "$path" == github.com/fastabc/fastconf/*) ]]; then
      candidates+=("$path@$version")
    fi
  done < <(awk '$1 == "require" {print $2, $3; next} {print $1, $2}' "$mod")
  [[ ${#candidates[@]} -gt 0 ]] || continue
  floor="$(awk '$1 == "go" {print $2; exit}' "$mod")"
  if [[ "$TOOLCHAIN" != auto* && "$TOOLCHAIN" != *+auto ]] && above_toolchain "$floor"; then
    echo "check-candidate: skip $module (go $floor > go $GOVER)"
    continue
  fi
  # Explicit queries record synthetic archive hashes; no-argument download
  # can leave only go.mod hashes. Avoid unrelated transitive archives via all.
  (cd "$TMP/checkout/$module" && go mod download "${candidates[@]}")
done <<< "$inventory"
# gorelease requires a clean VCS revision. Commit only this disposable copy,
# including its synthetic dependency sums; the source checkout is untouched.
git -C "$TMP/checkout" add --all
git -C "$TMP/checkout" -c user.name=Candidate -c user.email=candidate@example.invalid \
  -c core.hooksPath=/dev/null -c commit.gpgsign=false commit -qm candidate
cd "$TMP/checkout"
if [[ "$#" -gt 0 ]]; then
  "$@"
else
  bash tools/modules.sh test
fi
