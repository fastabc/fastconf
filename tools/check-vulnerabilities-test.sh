#!/usr/bin/env bash
# Scanner failures must survive tee, retain diagnostics and continue the inventory.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/repo/tools" "$TMP/repo/sub" "$TMP/bin"
cp "$ROOT/tools/check-vulnerabilities.sh" "$ROOT/tools/modules.sh" "$TMP/repo/tools/"
cd "$TMP/repo"
git init -q
printf 'module example.com/root\n\ngo 1.24\n' > go.mod
printf 'module example.com/sub\n\ngo 1.24\n' > sub/go.mod

cat > "$TMP/bin/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == install ]]
cp "$SCANNER_STUB" "$GOBIN/govulncheck"
chmod +x "$GOBIN/govulncheck"
STUB
cat > "$TMP/scanner" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == -version ]]; then echo 'test scanner'; exit; fi
printf '%s\n' "$PWD" >> "$SCAN_LOG"
if [[ "$PWD" == */sub || "$VULN_MODE" == clean ]]; then
  echo 'No vulnerabilities found.'
elif [[ "$VULN_MODE" == stdlib ]]; then
  echo 'Your code is affected by 1 vulnerability from the Go standard library.' >&2
  exit 3
else
  echo 'scanner failure' >&2
  exit 2
fi
STUB
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH" SCANNER_STUB="$TMP/scanner" SCAN_LOG="$TMP/scanned"
for mode in clean stdlib other; do
  : > "$SCAN_LOG"
  result=0
  VULN_MODE="$mode" bash tools/check-vulnerabilities.sh > "$TMP/result" 2>&1 || result=$?
  [[ "$(wc -l < "$SCAN_LOG" | tr -d ' ')" == 2 ]]
  if [[ "$mode" == clean ]]; then
    [[ "$result" == 0 ]]
  else
    [[ "$result" != 0 ]]
  fi
  if [[ "$mode" == stdlib ]]; then
    grep -q 'ensure Go toolchain is updated' "$TMP/result"
  elif grep -q 'ensure Go toolchain is updated' "$TMP/result"; then
    echo 'standard-library hint reported for an unrelated result' >&2
    exit 1
  fi
done
echo 'check-vulnerabilities: OK'
