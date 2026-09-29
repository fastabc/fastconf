#!/usr/bin/env bash
# Exercise discovery and immutable, coordinated tags in a disposable repository.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir "$TMP/repo"
cd "$TMP/repo"
git init -q
git config user.name test
git config user.email test@example.invalid
mkdir -p tools sub
cp "$ROOT/tools/tag-release.sh" "$ROOT/tools/modules.sh" tools/
printf 'module example.com/test\n\ngo 1.24.0\n' > go.mod
printf 'module example.com/test/sub\n\ngo 1.24.0\n\nrequire example.com/test v1.0.0\n' > sub/go.mod
git add .
git commit -qm initial
expect_failure() {
  if bash tools/tag-release.sh "$@" > "$TMP/output" 2>&1; then
    echo "unexpected success: $*" >&2; exit 1
  fi
  rm "$TMP/output"
}
for version in bad v01.0.0 v1.0.0-01 v1.0.0+meta; do expect_failure "$version"; done
expect_failure v1.1.0 --dry-run
[[ -z "$(git tag)" ]]
bash tools/tag-release.sh v1.0.0 --dry-run >/dev/null
[[ -z "$(git tag)" ]]
bash tools/tag-release.sh v1.0.0 >/dev/null
[[ "$(git tag | wc -l | tr -d ' ')" == 2 ]]
# Same commit is idempotent; force/delete workflows are deliberately absent.
bash tools/tag-release.sh v1.0.0 >/dev/null
expect_failure v1.0.0 --force
printf 'untracked\n' > pending
expect_failure v1.0.0
rm pending
# A newly added module is discovered without editing an inventory file.
mkdir new
printf 'module example.com/test/new\n\ngo 1.24.0\n' > new/go.mod
[[ "$(bash tools/modules.sh list | wc -l | tr -d ' ')" == 3 ]]
git add new
git commit -qm next
expect_failure v1.0.0
[[ "$(git tag | wc -l | tr -d ' ')" == 2 ]]
python3 - <<'PY'
from pathlib import Path
p=Path('sub/go.mod');p.write_text(p.read_text().replace('v1.0.0','v1.1.0'))
PY
git add sub/go.mod
git commit -qm version
bash tools/tag-release.sh v1.1.0 >/dev/null
[[ "$(git tag | wc -l | tr -d ' ')" == 5 ]]
echo 'tag-release: OK'
