#!/usr/bin/env bash
# Formatting checks must handle file moves before staging and inspect new files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools"
cp "$ROOT/tools/check-repo.sh" "$TMP/repo/tools/"
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
echo 'check-repo: OK'
