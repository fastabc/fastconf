#!/usr/bin/env bash
# Lint must discover modules, preserve command arguments and propagate failures.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools" "$TMP/repo/providers/new" "$TMP/repo/benchmarks/compare"
cp "$ROOT/tools/modules.sh" "$TMP/repo/tools/"
cd "$TMP/repo"
git init -q
for module in . providers/new benchmarks/compare; do
  printf 'module example.com/test\n\ngo 1.24\n' > "$module/go.mod"
done
touch .golangci.yml

cat > "$TMP/lint stub" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$GOWORK" == off && "$1" == 'argument with spaces' && "$2" == run ]]
[[ "$3" == --config && -f "$4" && "$5" == --timeout=5m && "$6" == ./... ]]
printf '%s\n' "$PWD" >> "$LINT_LOG"
[[ "${FAIL_MODULE:-}" != "${PWD##*/}" ]]
STUB
export LINT_LOG="$TMP/lint.log"

bash tools/modules.sh lint -- bash "$TMP/lint stub" 'argument with spaces'
[[ "$(wc -l < "$LINT_LOG" | tr -d ' ')" == 3 ]]
grep -Fxq "$PWD/providers/new" "$LINT_LOG"
grep -Fxq "$PWD/benchmarks/compare" "$LINT_LOG"

: > "$LINT_LOG"
bash tools/modules.sh lint providers/new -- bash "$TMP/lint stub" 'argument with spaces'
[[ "$(cat "$LINT_LOG")" == "$PWD/providers/new" ]]

: > "$LINT_LOG"
if FAIL_MODULE=compare bash tools/modules.sh lint -- bash "$TMP/lint stub" 'argument with spaces'; then
  echo 'lint missed a module failure' >&2
  exit 1
fi
[[ "$(wc -l < "$LINT_LOG" | tr -d ' ')" == 3 ]]
if bash tools/modules.sh lint missing -- bash "$TMP/lint stub" > "$TMP/output" 2>&1; then
  echo 'lint accepted an unknown module' >&2
  exit 1
fi
grep -Fq 'unknown module: missing' "$TMP/output"
# Workspace setup must keep version-qualified replaces, argv and cleanup.
cat > go.mod <<'MOD'
module example.com/root

go 1.24.0

require example.com/root/provider v1.1.0
MOD
cat > providers/new/go.mod <<'MOD'
module example.com/root/provider

go 1.24.0

require (
 example.com/root v1.2.0
)
MOD
cat > "$TMP/workspace stub" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == 'argument with spaces' ]]
[[ "$GOWORK" != off && -f "$GOWORK" ]]
printf '%s' "$GOWORK" > "$WORKSPACE_LOG"
go work edit -json > "$REPLACE_LOG"
exit "${WORKSPACE_STATUS:-0}"
STUB
export WORKSPACE_LOG="$TMP/workspace.log" REPLACE_LOG="$TMP/replace.json"
bash tools/modules.sh exec bash "$TMP/workspace stub" 'argument with spaces'
[[ ! -e "$(cat "$WORKSPACE_LOG")" ]]
python3 - "$REPLACE_LOG" "$PWD" <<'PYTHON'
import json
import sys
from pathlib import Path
work = json.loads(Path(sys.argv[1]).read_text())
assert {(r["Old"]["Path"], r["Old"]["Version"]): r["New"]["Path"] for r in work["Replace"]} == {
    ("example.com/root", "v1.2.0"): sys.argv[2] + "/.",
    ("example.com/root/provider", "v1.1.0"): sys.argv[2] + "/providers/new",
}
PYTHON
if WORKSPACE_STATUS=7 bash tools/modules.sh exec bash "$TMP/workspace stub" 'argument with spaces'; then
  echo 'exec swallowed a workspace command failure' >&2
  exit 1
else
  [[ $? == 7 ]]
fi
[[ ! -e "$(cat "$WORKSPACE_LOG")" ]]
echo 'modules: OK'
