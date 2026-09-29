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
initial_commit="$(git rev-parse HEAD)"
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
[[ "$(git tag)" == $'sub/v1.0.0\nv1.0.0' ]]
for tag in sub/v1.0.0 v1.0.0; do
  [[ "$(git rev-list -n1 "$tag")" == "$initial_commit" ]]
  [[ "$(git cat-file -t "refs/tags/$tag")" == tag ]]
done
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
[[ "$(git tag)" == $'sub/v1.0.0\nv1.0.0' ]]
for tag in sub/v1.0.0 v1.0.0; do
  [[ "$(git rev-list -n1 "$tag")" == "$initial_commit" ]]
  [[ "$(git cat-file -t "refs/tags/$tag")" == tag ]]
done
python3 - <<'PY'
from pathlib import Path
p=Path('sub/go.mod');p.write_text(p.read_text().replace('v1.0.0','v1.1.0'))
PY
git add sub/go.mod
git commit -qm version
bash tools/tag-release.sh v1.1.0 >/dev/null
[[ "$(git tag)" == $'new/v1.1.0\nsub/v1.0.0\nsub/v1.1.0\nv1.0.0\nv1.1.0' ]]
for tag in new/v1.1.0 sub/v1.1.0 v1.1.0; do
  [[ "$(git rev-list -n1 "$tag")" == "$(git rev-parse HEAD)" ]]
done
# The root tag must arrive alone, after all satellite tags. GitHub suppresses
# tag push events when more than three tags are created in one push.
mkdir extra
printf 'module example.com/test/extra\n\ngo 1.24.0\n' > extra/go.mod
sed 's/v1.1.0/v1.2.0/g' sub/go.mod > "$TMP/go.mod"
mv "$TMP/go.mod" sub/go.mod
git add extra sub/go.mod
git commit -qm next-release
git init --bare -q "$TMP/remote.git"
git remote add origin "$TMP/remote.git"
# Also exercise user settings that could implicitly push extra annotated tags.
git config push.followTags true
cat > "$TMP/remote.git/hooks/pre-receive" <<'HOOK'
#!/usr/bin/env bash
set -euo pipefail
updates="$(cat)"
if [[ -f reject-satellites ]]; then exit 1; fi
if [[ "$updates" == *refs/tags/v1.2.0* ]]; then
  [[ ! -f reject-root ]]
  [[ "$(printf '%s\n' "$updates" | wc -l | tr -d ' ')" == 1 ]]
  for module in extra new sub; do
    git show-ref --verify --quiet "refs/tags/$module/v1.2.0"
  done
fi
HOOK
chmod +x "$TMP/remote.git/hooks/pre-receive"
touch "$TMP/remote.git/reject-satellites"
expect_failure v1.2.0 --push
[[ -z "$(git --git-dir="$TMP/remote.git" tag)" ]]
rm "$TMP/remote.git/reject-satellites"
touch "$TMP/remote.git/reject-root"
expect_failure v1.2.0 --push
[[ "$(git --git-dir="$TMP/remote.git" tag)" == $'extra/v1.2.0\nnew/v1.2.0\nsub/v1.2.0' ]]
rm "$TMP/remote.git/reject-root"
bash tools/tag-release.sh v1.2.0 --push > "$TMP/output" 2>&1
[[ "$(git --git-dir="$TMP/remote.git" tag)" == $'extra/v1.2.0\nnew/v1.2.0\nsub/v1.2.0\nv1.2.0' ]]
for tag in extra/v1.2.0 new/v1.2.0 sub/v1.2.0 v1.2.0; do
  [[ "$(git --git-dir="$TMP/remote.git" rev-list -n1 "$tag")" == "$(git rev-parse HEAD)" ]]
done
bash tools/tag-release.sh v1.2.0 --push > "$TMP/output" 2>&1
echo 'tag-release: OK'
