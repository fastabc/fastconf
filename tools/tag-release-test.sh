#!/usr/bin/env bash
# tag-release-test.sh — fixture test for changed-only tagging.
set -euo pipefail
SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/tag-release.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
cd "$TMP" && git init -q && git config user.email t@t && git config user.name t

mkdir -p sub tools && cp "$SCRIPT" tools/tag-release.sh
printf 'go 1.22\nuse (\n\t.\n\t./sub\n)\n' > go.work
printf 'module example.com/root\ngo 1.22\n' > go.mod
printf 'module example.com/root/sub\ngo 1.22\nrequire example.com/root v0.1.0\n' > sub/go.mod
git add -A && git commit -qm init
bash tools/tag-release.sh v0.1.0 >/dev/null           # both never tagged → both tag

echo x > rootonly.txt && git add -A && git commit -qm root-change
out="$(bash tools/tag-release.sh v0.2.0)"
echo "$out" | grep -q 'tag   v0.2.0'                  || { echo "FAIL: root not tagged"; exit 1; }
echo "$out" | grep -q 'skip  sub/v0.2.0  (unchanged'  || { echo "FAIL: unchanged sub was tagged"; exit 1; }

echo y > sub/f.txt && git add -A && git commit -qm sub-change
out="$(bash tools/tag-release.sh v0.3.0)"
echo "$out" | grep -q 'tag   sub/v0.3.0'              || { echo "FAIL: changed sub not tagged"; exit 1; }
echo "tag-release-test: OK"
