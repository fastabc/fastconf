#!/usr/bin/env bash
# Prove the real compatibility gate rejects removed exports in disposable copies.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
GOBIN="$TMP" GOWORK=off go install golang.org/x/exp/cmd/gorelease@v0.0.0-20260908205506-85c1c2202aba
python3 - "$ROOT" "$TMP/repo" <<'PY'
from pathlib import Path
import shutil
import subprocess
import sys
root, dest = map(Path, sys.argv[1:])
for name in subprocess.check_output(["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"]).decode().split("\0"):
    src = root / name
    if not name or not src.is_file() or src.is_symlink() or name.startswith("benchmarks/"):
        continue
    target = dest / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, target)
for path, old, new in [("plan.go", "func WithPlanHostname(", "func withPlanHostname("),
                       ("integrations/cli/pflag/pflag.go", "func FromChanged(", "func fromChanged(")]:
    p = dest / path
    assert old in p.read_text()
    p.write_text(p.read_text().replace(old, new))
PY
git -C "$TMP/repo" init -q
git -C "$TMP/repo" add --all
git -C "$TMP/repo" -c user.name=Compatibility -c user.email=compatibility@example.invalid \
  -c core.hooksPath=/dev/null -c commit.gpgsign=false commit -qm 'negative compatibility fixtures'
for module in . integrations/cli/pflag; do
  if (cd "$TMP/repo/$module" && GOWORK=off "$TMP/gorelease" -base=v1.1.0 -version=v1.2.0) > "$TMP/result" 2>&1; then
    echo "compatibility gate accepted a removed export in $module" >&2
    exit 1
  fi
  symbol=WithPlanHostname
  [[ "$module" == . ]] || symbol=FromChanged
  # A network/build error alone is not evidence of compatibility enforcement.
  if ! grep -E "$symbol.*removed|removed.*$symbol" "$TMP/result"; then
    cat "$TMP/result" >&2
    exit 1
  fi
done
echo 'check-api-negative: removed root and satellite exports rejected'
