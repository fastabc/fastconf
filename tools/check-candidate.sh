#!/usr/bin/env bash
# Validate the unpublished v1.0.0 modules through the module protocol. All synthetic
# archives, downloaded modules and generated sums live in a disposable checkout.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'chmod -R u+w "$TMP"; rm -rf "$TMP"' EXIT
VERSION="${VERSION:-v1.0.0}"


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
    module = owner(name)
    path = "github.com/fastabc/fastconf" + ("" if module == "." else "/" + module)
    if path not in archives:
        proxy = temp / "proxy" / path / "@v"
        proxy.mkdir(parents=True)
        archives[path] = (zipfile.ZipFile(proxy / (version + ".zip"), "w", zipfile.ZIP_DEFLATED), proxy, module)
    rel = name if module == "." else name[len(module) + 1:]
    archives[path][0].write(source, path + "@" + version + "/" + rel)
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
export GOWORK=off
export GOMODCACHE="$TMP/modcache"
export GOPROXY="file://$TMP/proxy,${GOPROXY:-https://proxy.golang.org,direct}"
export GONOSUMDB=github.com/fastabc/fastconf
export GOPRIVATE=
export GONOPROXY=
echo "check-candidate: $VERSION from checkout; isolated proxy/cache; GOWORK=off"
# Floor jobs pin an old toolchain; modules above it cannot load there and are
# not under test, so leave their checksums unfilled.
GOVER="$(go env GOVERSION)"; GOVER="${GOVER#go}"
pinned_toolchain() { [[ "$(go env GOTOOLCHAIN)" != auto* ]]; }
while IFS= read -r module; do
  mod="$TMP/checkout/$module/go.mod"
  grep -Eq "github\.com/fastabc/fastconf(/[^[:space:]]+)?[[:space:]]+$VERSION([[:space:]]|$)" "$mod" || continue
  floor="$(awk '$1 == "go" {print $2; exit}' "$mod")"
  if pinned_toolchain && [[ "$(printf '%s\n' "$floor" "$GOVER" | sort -V | tail -n1)" != "$GOVER" ]]; then
    echo "check-candidate: skip $module (go $floor > go $GOVER)"
    continue
  fi
  (cd "$TMP/checkout/$module" && go mod download all)
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
