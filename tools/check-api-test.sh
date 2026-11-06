#!/usr/bin/env bash
# Exercise the real gorelease against published versions in a local fixture proxy.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'chmod -R u+w "$TMP"; rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools" "$TMP/bin" "$TMP/tool"
cp "$ROOT/tools/check-api.sh" "$ROOT/tools/check-candidate.sh" "$ROOT/tools/modules.sh" "$TMP/repo/tools/"
GOBIN="$TMP/tool" GOWORK=off go install golang.org/x/exp/cmd/gorelease@v0.0.0-20260908205506-85c1c2202aba

# Reuse the real binary while keeping fixture modules and downloads offline.
REAL_GO="$(command -v go)"
export REAL_GO API_TEST_GORELEASE="$TMP/tool/gorelease"
cat > "$TMP/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == install ]]; then
  cp "$API_TEST_GORELEASE" "$GOBIN/gorelease"
else
  exec "$REAL_GO" "$@"
fi
SH
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH" GOPROXY="file://$TMP/proxy" GOMODCACHE="$TMP/modcache"
export GOWORK=off GOFLAGS=-mod=readonly GONOSUMDB=github.com/fastabc/fastconf GONOPROXY=none
python3 - "$TMP" <<'PY'
import json
from pathlib import Path
import sys
import zipfile

temp = Path(sys.argv[1])
for name in ("", "sub", "new"):
    path = "github.com/fastabc/fastconf" + ("/" + name if name else "")
    directory = temp / "repo" / name
    directory.mkdir(exist_ok=True)
    manifest = f"module {path}\n\ngo 1.24.0\n"
    source = "package fixture\nconst Kept = 1\n"
    dependency = "\nrequire github.com/fastabc/fastconf v1.2.0\n" if name == "sub" else ""
    if name == "sub":
        source = 'package fixture\nimport _ "github.com/fastabc/fastconf"\nconst Kept = 1\n'
    (directory / "go.mod").write_text(manifest + dependency)
    (directory / "api.go").write_text(source)
    proxy = temp / "proxy" / path / "@v"
    proxy.mkdir(parents=True)
    versions = ["v1.2.0", "v1.2.0-rc.1"]
    if name != "new":
        versions.insert(0, "v1.1.0")
    (proxy / "list").write_text("\n".join(versions) + "\n")
    for version in versions:
        published_manifest = manifest + dependency.replace("v1.2.0", version)
        (proxy / f"{version}.mod").write_text(published_manifest)
        (proxy / f"{version}.info").write_text(json.dumps({
            "Version": version, "Time": "2026-09-28T00:00:00Z",
        }))
        with zipfile.ZipFile(proxy / f"{version}.zip", "w") as archive:
            archive.writestr(f"{path}@{version}/go.mod", published_manifest)
            published = source
            if not name and version != "v1.1.0":
                published += "const Added = 2\n"
            archive.writestr(f"{path}@{version}/api.go", published)
PY
cd "$TMP/repo"
git init -q
git config user.name test
git config user.email test@example.invalid
git config commit.gpgsign false
git config core.hooksPath /dev/null
git add go.mod api.go sub tools
git commit -qm baseline
git tag v1.1.0
printf 'package fixture\nconst Kept = 1\nconst Added = 2\n' > api.go
git add .
git commit -qm candidate
git tag v1.2.0
unset API_VERSION
export BASE=v1.1.0 VERSION=v1.2.0

check_success() {
  if ! bash tools/check-candidate.sh --api > "$TMP/result" 2>&1; then
    cat "$TMP/result" >&2; exit 1
  fi
  grep -Fq "gorelease: sub (base=$BASE," "$TMP/result"
  if [[ "$BASE" == v1.1.0 ]]; then
    grep -Fq 'Added: added' "$TMP/result"
    grep -Fq 'gorelease: new (base=none,' "$TMP/result"
  else
    grep -Fq "gorelease: new (base=$BASE," "$TMP/result"
  fi
  git diff --exit-code
}
check_failure() {
  if bash tools/check-candidate.sh --api > "$TMP/result" 2>&1; then
    echo 'API check unexpectedly succeeded' >&2; exit 1
  fi
  if ! grep -Fq "$1" "$TMP/result"; then
    cat "$TMP/result" >&2; exit 1
  fi
}
# Both stable and prerelease candidates may already have been published.
check_success
# Makefile and CI explicitly set API_VERSION, even when the tag already exists.
API_VERSION=v1.2.0 check_success
go -C sub mod edit -require=github.com/fastabc/fastconf@v1.2.0-rc.1
git add .
git commit -qm prerelease
VERSION=v1.2.0-rc.1 check_success
VERSION=v1.2.0-rc.1 API_VERSION=v1.2.0-rc.1 check_success
go -C sub mod edit -require=github.com/fastabc/fastconf@v1.2.0
git add .
git commit -qm stable
# Comparing the checkout against its already-published version must also work.
BASE=v1.2.0 check_success
BASE=v1.2.0 API_VERSION=v1.2.0 check_success
# A new minor API may be checked while manifests still pin the published version.
printf 'const Next = 3\n' >> api.go
git add .
git commit -qm next-api
BASE=v1.2.0 API_VERSION=v1.3.0 check_success
grep -Fq 'version=v1.3.1-api-check)' "$TMP/result"
BASE=v1.2.0 API_VERSION=v1.2.1 check_failure 'minor version is not incremented'
# Retain minor-version enforcement and reject older candidates.
VERSION=v1.1.1 check_failure 'minor version is not incremented'
VERSION=v1.0.9 check_failure 'must be lower than release version'
# Missing published baselines must fail, even when a local tag exists.
git tag v1.0.1
BASE=v1.0.1 check_failure 'v1.0.1'
# A published target must not mask removed exports in root or satellite APIs.
printf 'package fixture\n' > api.go
printf 'package fixture\n' > sub/api.go
git add .
git commit -qm incompatible
check_failure 'Kept: removed'
[[ "$(grep -c 'Kept: removed' "$TMP/result")" == 2 ]]
BASE=v1.2.0 check_failure 'Kept: removed'
[[ "$(grep -c 'Kept: removed' "$TMP/result")" == 2 ]]
API_VERSION=v1.3.0 check_failure 'Kept: removed'
[[ "$(grep -c 'Kept: removed' "$TMP/result")" == 2 ]]
echo 'check-api: OK'
