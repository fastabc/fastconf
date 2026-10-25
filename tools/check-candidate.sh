#!/usr/bin/env bash
# Validate candidate modules through the module protocol. All synthetic
# archives, downloaded modules and generated sums live in a disposable checkout.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'chmod -R u+w "$TMP"; rm -rf "$TMP"' EXIT
VERSION="${VERSION:-v1.2.0}"

python3 - "$ROOT" "$TMP" "$VERSION" <<'PYTHON'
import json
import pathlib
import shutil
import subprocess
import sys
import zipfile

root, temp = map(pathlib.Path, sys.argv[1:3])
version = sys.argv[3]
checkout = temp / "checkout"
modules = subprocess.check_output(["bash", str(root / "tools/modules.sh"), "list"], text=True).splitlines()
nested = [m for m in modules if m != "."]
candidate_sums = {
    ("github.com/fastabc/fastconf" + ("" if m == "." else "/" + m), v)
    for m in modules for v in (version, version + "/go.mod")
}
files = subprocess.check_output([
    "git", "-C", str(root), "ls-files", "--cached", "--others",
    "--exclude-standard", "-z",
]).decode().split("\0")

def owner(name):
    """Innermost inventory module containing name."""
    return max((m for m in nested if name.startswith(m + "/")), key=len, default=".")

# Every inventory module is served at the candidate version, so satellites may
# pin each other (e.g. log adapters -> integrations/log) before publication.
archives = {}
for name in sorted(set(files)):
    source = root / name
    if not name or not source.is_file() or source.is_symlink():
        continue
    target = checkout / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
    # Retain the standalone comparison workspace, but never ship it inside
    # the root module's synthetic archive or treat it as a release module.
    if name.startswith("benchmarks/"):
        continue
    module = owner(name)
    if name == ("go.sum" if module == "." else module + "/go.sum"):
        # Published sums describe different archives from this synthetic proxy.
        # Regenerate only the discovered candidates, in the disposable copy;
        # retain third-party checksums and other versions for verification.
        target.write_text("".join(
            line for line in target.read_text().splitlines(keepends=True)
            if tuple(line.split()[:2]) not in candidate_sums
        ))
    path = "github.com/fastabc/fastconf" + ("" if module == "." else "/" + module)
    if path not in archives:
        proxy = temp / "proxy" / path / "@v"
        proxy.mkdir(parents=True)
        archives[path] = (zipfile.ZipFile(proxy / (version + ".zip"), "w", zipfile.ZIP_DEFLATED), proxy, module)
    rel = name if module == "." else name[len(module) + 1:]
    archives[path][0].write(target, path + "@" + version + "/" + rel)
for path, (archive, proxy, module) in archives.items():
    archive.close()
    shutil.copy2(root / module / "go.mod", proxy / (version + ".mod"))
    (proxy / (version + ".info")).write_text(json.dumps({
        "Version": version, "Time": "2026-09-28T00:00:00Z",
    }))
    # Do not serve a version list or @latest: gorelease's baseline must resolve
    # from the published upstream proxy, never from this synthetic candidate.
PYTHON

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
done < <(bash "$TMP/checkout/tools/modules.sh" list)
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
