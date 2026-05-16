#!/usr/bin/env bash
# code-review-graph.sh
# 生成包依赖图与 CODEOWNERS owner-graph，供 reviewer 快速定位。
# 输出: docs/graph/{deps.dot,deps.svg,owners.md}
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/docs/graph"
mkdir -p "$OUT"

# 1) 包依赖图（DOT）
{
  echo 'digraph fastconf_deps {'
  echo '  rankdir=LR; node [shape=box,fontname="Helvetica"];'
  cd "$ROOT"
  go list -deps -f '{{ .ImportPath }} {{ join .Imports "," }}' ./... \
    | grep '^github.com/fastabc/fastconf' \
    | while read -r pkg deps; do
        IFS=',' read -ra arr <<<"$deps"
        for d in "${arr[@]}"; do
          [[ "$d" == github.com/fastabc/fastconf* ]] || continue
          echo "  \"${pkg##github.com/fastabc/fastconf/}\" -> \"${d##github.com/fastabc/fastconf/}\";"
        done
      done
  echo '}'
} > "$OUT/deps.dot"

if command -v dot >/dev/null; then
  dot -Tsvg "$OUT/deps.dot" -o "$OUT/deps.svg"
  echo "wrote $OUT/deps.svg"
else
  echo "graphviz 'dot' not found; skipped svg render" >&2
fi

# 2) Owner speed-reference table (only when .github/CODEOWNERS exists)
if [ -f "$ROOT/.github/CODEOWNERS" ]; then
  {
    echo "# Code Owners Graph"
    echo
    echo "| Path | Owners |"
    echo "|---|---|"
    awk '!/^#/ && NF>=2 {printf "| `%s` | %s |\n", $1, substr($0,index($0,$2))}' "$ROOT/.github/CODEOWNERS"
  } > "$OUT/owners.md"
else
  echo "skip: .github/CODEOWNERS not found; owners.md not generated" >&2
fi

echo "done."
