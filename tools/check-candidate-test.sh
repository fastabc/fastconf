#!/usr/bin/env bash
# Candidate sums must be isolated without dropping unrelated dependency checks.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'chmod -R u+w "$TMP"; rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools" "$TMP/repo/sub"
cp "$ROOT/tools/check-candidate.sh" "$ROOT/tools/modules.sh" "$TMP/repo/tools/"
cd "$TMP/repo"
git init -q
printf 'module github.com/fastabc/fastconf\n\ngo 1.24.0\n' > go.mod
printf 'package fastconf\nconst Value = "candidate"\n' > config.go
cat > sub/go.mod <<'MOD'
module github.com/fastabc/fastconf/sub

go 1.24.0

require github.com/fastabc/fastconf v1.1.0
MOD
cat > sub/config_test.go <<'GO'
package sub

import (
    "testing"
    "github.com/fastabc/fastconf"
)

func TestCandidate(t *testing.T) {
    if fastconf.Value != "candidate" { t.Fatal("did not use candidate source") }
}
GO
cat > go.sum <<'SUM'
github.com/fastabc/fastconf v1.1.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
github.com/fastabc/fastconf v1.1.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
github.com/fastabc/fastconf/sub v1.1.0 h1:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE=
github.com/fastabc/fastconf/sub v1.1.0/go.mod h1:FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=
github.com/fastabc/fastconf v1.0.0 h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
github.com/fastabc/fastconf v1.0.0/go.mod h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=
github.com/fastabc/fastconf v0.20.0 h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
github.com/fastabc/fastconf/external v1.1.0 h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=
example.com/third-party v1.0.0 h1:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=
SUM
cp go.sum sub/go.sum
mkdir testdata
cp go.sum testdata/go.sum
cp go.sum "$TMP/original.sum"
git add .
# Comparison modules must not enter release discovery or candidate archives.
mkdir -p benchmarks/compare
printf 'module example.com/comparison\n\ngo 1.24.0\n' > benchmarks/compare/go.mod
[[ "$(bash tools/modules.sh list | wc -l | tr -d ' ')" == 2 ]]
# No network fallback: both candidate modules must resolve from the file proxy.
GOPROXY=off VERSION=v1.1.0 bash tools/check-candidate.sh bash -c '
  set -euo pipefail
  go -C sub test -mod=readonly ./...
  python3 - <<"PY"
import os
from pathlib import Path
from urllib.parse import urlsplit, unquote
import zipfile

candidate_modules = (
    "github.com/fastabc/fastconf",
    "github.com/fastabc/fastconf/sub",
)
preserved = Path("go.sum").read_text().splitlines()
assert len(preserved) == 5, preserved
assert all(not line.startswith(module + " v1.1.0") for module in candidate_modules for line in preserved)
assert any(line.startswith("github.com/fastabc/fastconf v1.0.0 ") for line in preserved)
assert any(line.startswith("github.com/fastabc/fastconf v1.0.0/go.mod ") for line in preserved)
assert any(line.startswith("github.com/fastabc/fastconf/external v1.1.0 ") for line in preserved)
assert all(line in Path("sub/go.sum").read_text().splitlines() for line in preserved)
assert "h1:AAAAAAAA" not in Path("sub/go.sum").read_text()
assert "h1:EEEE" not in Path("sub/go.sum").read_text()
proxy = Path(unquote(urlsplit(os.environ["GOPROXY"].split(",")[0]).path))
for module in ("github.com/fastabc/fastconf", "github.com/fastabc/fastconf/sub"):
    with zipfile.ZipFile(proxy / module / "@v/v1.1.0.zip") as archive:
        lines = archive.read(module + "@v1.1.0/go.sum").decode().splitlines()
        assert lines == preserved, (module, lines)
        if module == "github.com/fastabc/fastconf":
            assert not any("/benchmarks/" in name for name in archive.namelist())
            fixture = archive.read(module + "@v1.1.0/testdata/go.sum").decode()
            assert fixture == Path("testdata/go.sum").read_text()
            assert "h1:AAAAAAAA" in fixture
            assert "h1:EEEE" in fixture
PY
'
cmp go.sum "$TMP/original.sum"
cmp sub/go.sum "$TMP/original.sum"
[[ -z "$(git diff --name-only)" ]]
# Explicit API baselines must survive the isolated checkout, including evidence
# that a newly introduced module was absent from the baseline tree.
git -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -qm baseline
git tag v1.0.0
mkdir added
printf 'module github.com/fastabc/fastconf/added\n\ngo 1.24.0\n' > added/go.mod
BASE=v1.0.0 GOPROXY=off VERSION=v1.1.0 bash tools/check-candidate.sh bash -c '
  set -euo pipefail
  git cat-file -e v1.0.0:sub/go.mod
  test -f added/go.mod
  if git cat-file -e v1.0.0:added/go.mod 2>/dev/null; then
    echo "new module unexpectedly present in baseline" >&2; exit 1
  fi
'
if BASE=v9.9.9 GOPROXY=off VERSION=v1.1.0 bash tools/check-candidate.sh true > "$TMP/missing-baseline" 2>&1; then
  echo "missing baseline tag was accepted" >&2; exit 1
fi
echo 'check-candidate: OK'
