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
  manager.go options.go errors.go state.go defaults.go secret.go presets.go \
  registry.go tenant.go pipeline.go pipeline_stages.go provider_watch.go \
  obs_audit.go obs_metrics.go obs_tracer.go watch.go watcher.go doc.go feature.go introspect.go \
  field_meta.go secret_resolver.go bind.go
do
  [ -f "$ROOT/$f" ] || error "root/$f"
done

# pkg/ packages
for d in decoder discovery mappath merger migration parser profile provider source transform validate; do
  [ -d "$ROOT/pkg/$d" ] || error "pkg/$d"
done

# internal/ packages
for d in coalesce obs typeinfo watcher; do
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

# tools/
[ -f "$ROOT/tools/loc-budget.sh" ]         || error "tools/loc-budget.sh"
[ -f "$ROOT/tools/total-loc-budget.sh" ]   || error "tools/total-loc-budget.sh"
[ -f "$ROOT/tools/bench-guard.sh" ]        || error "tools/bench-guard.sh"
[ -f "$ROOT/tools/code-review-graph.sh" ]  || error "tools/code-review-graph.sh"

# cmd/
[ -d "$ROOT/cmd/fastconfd" ]  || error "cmd/fastconfd"
[ -d "$ROOT/cmd/fastconfctl" ] || error "cmd/fastconfctl"
[ -d "$ROOT/cmd/fastconfgen" ] || error "cmd/fastconfgen"

if [ "$FAIL" -ne 0 ]; then
  echo "check-layout: FAILED — some declared files/directories are missing" >&2
  exit 1
fi
echo "check-layout: OK"
