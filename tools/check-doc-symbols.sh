#!/usr/bin/env bash
# check-doc-symbols.sh — verify that key public symbols declared in README.md
# actually exist in the codebase. Uses ripgrep (rg) or grep as fallback.
# Fails with a non-zero exit code when a declared symbol is missing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FAIL=0
error() { echo "MISSING SYMBOL: $1" >&2; FAIL=1; }

RG=""
if command -v rg >/dev/null 2>&1; then
  RG=rg
fi

# Check that a Go identifier exists somewhere in the root package source.
check_symbol() {
  local sym="$1"
  if [ -n "$RG" ]; then
    $RG -q --type go "$sym" "$ROOT" && return 0
  else
    grep -rq "$sym" "$ROOT"/*.go 2>/dev/null && return 0
    grep -rqR "$sym" "$ROOT/pkg/" "$ROOT/contracts/" 2>/dev/null && return 0
  fi
  return 1
}

must_exist() {
  local sym="$1"
  check_symbol "$sym" || error "$sym"
}

# Core Manager symbols. Root-level Manager[T] wraps internal/manager.M[T];
# the wrapper exists in fastconf/manager.go while the implementation
# methods live on *M[T] in internal/manager.
must_exist "type Manager\[T any\]"
must_exist "func.*\*M\[T\].*Get\(\)"
must_exist "func.*\*M\[T\].*Reload\("
must_exist "func.*\*M\[T\].*Plan\("
must_exist "func.*\*M\[T\].*Snapshot\("
must_exist "func.*\*M\[T\].*Close\("
must_exist "func.*\*M\[T\].*Errors\("
must_exist "func.*\*M\[T\].*Watcher\("
must_exist "func.*\*M\[T\].*Replay\("
must_exist "func Subscribe\["
must_exist "func Eval\["

# State / ReloadCause
must_exist "type State\[T any\]"
must_exist "type ReloadCause struct"

# PlanResult
must_exist "type PlanResult\[T any\]"
must_exist "Validators.*\[\]ValidatorReport"

# Key options
must_exist "WithDir\("
must_exist "WithWatch\("
must_exist "WithValidator\["
must_exist "WithPolicy\["
must_exist "WithAuditSink\("
must_exist "WithProvenance\("

# Codec registration (fastconf package, not contracts)
must_exist "func RegisterCodec\("
must_exist "func RegisterCodecExt\("

# Check that ghost APIs are absent
if check_symbol "ManualReload"; then
  echo "GHOST SYMBOL FOUND: ManualReload (should not exist in production code)" >&2
  FAIL=1
fi
if check_symbol "RequestID.*ManualReload\|ManualReload.*RequestID"; then
  echo "GHOST SYMBOL FOUND: ManualReload/RequestID coupling (should not exist)" >&2
  FAIL=1
fi
if check_symbol "func.*Manager\[T\].*Watch\("; then
  echo "GHOST SYMBOL FOUND: Manager.Watch (use Subscribe instead)" >&2
  FAIL=1
fi

# Cookbook symbol audit. Every `fastconf.<Ident>` reference in
# docs/cookbook/*.md must resolve to a Go identifier in the root
# package (or a subpackage). This catches stale recipes after public-API
# renames — e.g. the v0.18 Sub → Extract migration would have left
# docs/cookbook/introspect.md referencing a non-existent symbol.
COOKBOOK_DIR="$ROOT/docs/cookbook"
COOKBOOK_FAIL=0
if [ -d "$COOKBOOK_DIR" ]; then
  if [ -n "$RG" ]; then
    cookbook_syms=$($RG --no-filename -o 'fastconf\.[A-Z][A-Za-z0-9_]*' "$COOKBOOK_DIR" 2>/dev/null \
      | sed 's/^fastconf\.//' | sort -u)
  else
    cookbook_syms=$(grep -hoE 'fastconf\.[A-Z][A-Za-z0-9_]*' "$COOKBOOK_DIR"/*.md 2>/dev/null \
      | sed 's/^fastconf\.//' | sort -u)
  fi
  for sym in $cookbook_syms; do
    # Boundary anchors keep WithDir from matching WithDirNested, etc.
    # We accept the symbol in any .go file under the repo.
    if [ -n "$RG" ]; then
      $RG -q --type go "\\b${sym}\\b" "$ROOT" || {
        echo "COOKBOOK GHOST SYMBOL: fastconf.${sym} (referenced in docs/cookbook/ but not defined in *.go)" >&2
        COOKBOOK_FAIL=1
      }
    else
      grep -rq -E "\\b${sym}\\b" "$ROOT"/*.go "$ROOT"/pkg "$ROOT"/contracts 2>/dev/null || {
        echo "COOKBOOK GHOST SYMBOL: fastconf.${sym} (referenced in docs/cookbook/ but not defined in *.go)" >&2
        COOKBOOK_FAIL=1
      }
    fi
  done
fi
if [ "$COOKBOOK_FAIL" -ne 0 ]; then
  FAIL=1
fi

if [ "$FAIL" -ne 0 ]; then
  echo "check-doc-symbols: FAILED" >&2
  exit 1
fi
echo "check-doc-symbols: OK"
