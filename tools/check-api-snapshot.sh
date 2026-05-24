#!/usr/bin/env bash
# check-api-snapshot.sh — detect accidental root public API drift.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SNAPSHOT="$ROOT/tools/api/fastconf.txt"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

(cd "$ROOT" && go run ./tools/api-snapshot) > "$TMP"

if [ "${UPDATE_API_SNAPSHOT:-}" = "1" ]; then
  mkdir -p "$(dirname "$SNAPSHOT")"
  cp "$TMP" "$SNAPSHOT"
  echo "check-api-snapshot: updated $SNAPSHOT"
  exit 0
fi

if [ ! -f "$SNAPSHOT" ]; then
  echo "check-api-snapshot: missing $SNAPSHOT; run UPDATE_API_SNAPSHOT=1 bash tools/check-api-snapshot.sh" >&2
  exit 1
fi

if ! diff -u "$SNAPSHOT" "$TMP"; then
  echo "check-api-snapshot: exported root API changed" >&2
  echo "  If this is intentional, update CHANGELOG/migration docs, then run:" >&2
  echo "  UPDATE_API_SNAPSHOT=1 bash tools/check-api-snapshot.sh" >&2
  exit 1
fi
echo "check-api-snapshot: OK"
