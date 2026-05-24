#!/usr/bin/env bash
# check-layout.sh — verify that the key files and directories declared in
# README.md and CLAUDE.md actually exist. Fails fast with a non-zero exit
# code and a human-readable error message when a declared artifact is missing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FAIL=0
error() { echo "MISSING: $1" >&2; FAIL=1; }

# Root package files
for f in \
  aliases.go bind.go defaults.go doc.go errors.go feature.go \
  manager.go obs.go options.go presets.go registry.go state.go
do
  [ -f "$ROOT/$f" ] || error "root/$f"
done

# Top-level directories
while IFS= read -r dir; do
  [ -z "$dir" ] && continue
  [ -d "$ROOT/$dir" ] || error "$dir"
done < "$ROOT/tools/allowed-dirs.txt"

while IFS= read -r dir; do
  [ "$dir" = "$ROOT" ] && continue
  name="${dir#"$ROOT/"}"
  case "$name" in
    .|.git|.github|.idea|.code-review-graph|.worktrees|.claude|logs) continue ;;
  esac
  if ! grep -Fxq "$name/" "$ROOT/tools/allowed-dirs.txt"; then
    echo "UNEXPECTED: $name" >&2
    FAIL=1
  fi
done < <(find "$ROOT" -maxdepth 1 -type d)

# pkg/ packages
for d in cliadapter decoder discovery feature flog generator mappath merger migration parser profile provider source transform validate; do
  [ -d "$ROOT/pkg/$d" ] || error "pkg/$d"
done

# internal/ packages
for d in coalesce diffreport fcerr fctypes manager obs options pipeline provenance registry secret state tenant testutil typeinfo watcher; do
  [ -d "$ROOT/internal/$d" ] || error "internal/$d"
done

# contracts/
[ -d "$ROOT/contracts" ] || error "contracts/"

# integrations/
[ -d "$ROOT/integrations/bus" ]    || error "integrations/bus"
[ -d "$ROOT/integrations/render" ] || error "integrations/render"

# providers/
[ -d "$ROOT/providers/consul" ] || error "providers/consul"
[ -d "$ROOT/providers/vault" ]  || error "providers/vault"
[ -d "$ROOT/providers/http" ]   || error "providers/http"
[ -d "$ROOT/providers/k8s" ]    || error "providers/k8s"
[ -d "$ROOT/providers/nats" ]   || error "providers/nats"
[ -d "$ROOT/providers/redisstream" ] || error "providers/redisstream"
[ -d "$ROOT/providers/s3" ]     || error "providers/s3"

# tools/
[ -f "$ROOT/tools/loc-budget.sh" ]         || error "tools/loc-budget.sh"
[ -f "$ROOT/tools/total-loc-budget.sh" ]   || error "tools/total-loc-budget.sh"
[ -f "$ROOT/tools/bench-guard.sh" ]        || error "tools/bench-guard.sh"
[ -f "$ROOT/tools/code-review-graph.sh" ]  || error "tools/code-review-graph.sh"
[ -f "$ROOT/tools/check-api-snapshot.sh" ] || error "tools/check-api-snapshot.sh"
[ -f "$ROOT/tools/check-module-matrix.sh" ] || error "tools/check-module-matrix.sh"

# cmd/
[ -d "$ROOT/cmd/fastconfd" ]  || error "cmd/fastconfd"
[ -d "$ROOT/cmd/fastconfctl" ] || error "cmd/fastconfctl"
[ -d "$ROOT/cmd/fastconfgen" ] || error "cmd/fastconfgen"

if [ "$FAIL" -ne 0 ]; then
  echo "check-layout: FAILED — some declared files/directories are missing" >&2
  exit 1
fi
echo "check-layout: OK"
