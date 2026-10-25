#!/usr/bin/env bash
# Formatting checks must handle file moves before staging and inspect new files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools"
cp "$ROOT/tools/check-repo.sh" "$ROOT/tools/check-doc-links.py" "$TMP/repo/tools/"
cd "$TMP/repo"
git init -q
printf 'package example\n' > old.go
git add old.go
rm old.go
bash tools/check-repo.sh fmt
mkdir 'new package'
printf 'package example\nfunc value()int{return 1}\n' > 'new package/new.go'
if bash tools/check-repo.sh fmt > "$TMP/output" 2>&1; then
  echo 'format check missed an untracked source' >&2
  exit 1
fi
grep -Fq 'new package/new.go' "$TMP/output"
gofmt -w 'new package/new.go'
bash tools/check-repo.sh fmt

# Formatter arguments must preserve file names and propagate check failures.
cat > "$TMP/formatter stub" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == fmt && "$2" == --config && "$4" == --diff ]]
[[ $# == 5 && "$5" == 'new package/new.go' ]]
exit "${FORMAT_STATUS:-0}"
STUB
bash tools/check-repo.sh fmt bash "$TMP/formatter stub"
if FORMAT_STATUS=1 bash tools/check-repo.sh fmt bash "$TMP/formatter stub"; then
  echo 'format check swallowed formatter failure' >&2
  exit 1
fi
printf '[source](new%%20package/new.go)\n' > README.md
bash tools/check-repo.sh docs
printf '[broken](missing.md)\n' >> README.md
if bash tools/check-repo.sh docs > "$TMP/output" 2>&1; then
  echo 'doc check missed a local file link' >&2
  exit 1
fi
grep -Fq 'README.md: missing missing.md' "$TMP/output"
echo 'check-repo: OK'
