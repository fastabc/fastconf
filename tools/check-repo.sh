#!/usr/bin/env bash
# Source checks shared by local development and CI.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
case "${1:-}" in
  fmt)
    sources=()
    while IFS= read -r -d '' file; do
      [[ ! -f "$file" ]] || sources+=("$file")
    done < <(git ls-files --cached --others --exclude-standard -z -- '*.go')
    [[ ${#sources[@]} -gt 0 ]] || exit 0
    files="$(gofmt -l "${sources[@]}")"
    if [[ -n "$files" ]]; then printf 'Files need gofmt:\n%s\n' "$files" >&2; exit 1; fi
    # Pass the pinned formatter as an argument vector, preserving spaces in paths.
    # Explicit files include examples hidden behind build tags and all modules.
    shift
    if [[ $# -gt 0 ]]; then
      "$@" fmt --config "$ROOT/.golangci.yml" --diff "${sources[@]}"
    fi
    ;;
  docs) python3 "$ROOT/tools/check-doc-links.py" ;;
  deps)
    for package in ./internal/coalesce ./integrations/log/internal/...; do
      deps="$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "$package")"
      prefix="github.com/fastabc/fastconf/${package#./}"
      prefix="${prefix%/...}"
      unexpected="$(printf '%s\n' "$deps" | awk -v prefix="$prefix" '$0 != prefix && index($0, prefix "/") != 1')"
      if [[ -n "$unexpected" ]]; then
        printf '%s must use only the standard library and internal helpers:\n%s\n' "$package" "$unexpected" >&2
        exit 1
      fi
    done
    ;;
  *) echo "usage: $0 fmt [formatter command ...]|deps|docs" >&2; exit 2 ;;
esac
