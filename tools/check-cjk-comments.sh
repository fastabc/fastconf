#!/usr/bin/env bash
# check-cjk-comments.sh — fail if any non-test .go file contains CJK
# characters in comments, identifiers, or string literals.
#
# Rationale: CJK characters in pkg/ and internal/ files get rendered
# directly onto pkg.go.dev's public godoc pages. Tests, docs/, logs/,
# and worktree shadows are exempt because they are not in the public
# godoc surface.
#
# Portable across macOS (BSD grep) and Linux (GNU grep) by using perl
# for the actual scan.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# CJK Unified Ideographs (U+4E00..U+9FFF). Extend the class if other
# ranges (Hiragana, Katakana, full-width punctuation) ever surface.
mapfile -t hits < <(
  /usr/bin/find . -name '*.go' \! -name '*_test.go' \
    \! -path './.git/*' \
    \! -path './.worktrees/*' \
    \! -path './logs/*' \
    \! -path './docs/*' \
    -print0 |
    xargs -0 perl -CSDA -ne 'print "$ARGV\n" if /[\x{4e00}-\x{9fff}]/' |
    sort -u
)

if (( ${#hits[@]} > 0 )); then
  echo "check-cjk-comments: non-test .go files contain CJK characters:" >&2
  printf '  %s\n' "${hits[@]}" >&2
  exit 1
fi

echo "check-cjk-comments: OK"
