#!/usr/bin/env bash
# Build release binaries and archive only the requested version/matrix.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
VERSION="${VERSION:-v1.1.0}"
COMMAND="${1:-}"
[[ $# == 0 ]] || shift

parse_target() {
  IFS=- read -r bin os arch extra <<< "$1"
  case "$bin" in fastconfd|fastconfctl|fastconfgen) ;; *) echo "unknown binary: $bin" >&2; exit 2 ;; esac
  [[ -n "$os" && -n "$arch" && -z "$extra" ]] || { echo "invalid target: $1" >&2; exit 2; }
  ext=""; suffix=tar.gz
  if [[ "$os" == windows ]]; then ext=.exe; suffix=zip; fi
  name="${bin}_${VERSION}_${os}_${arch}"
}
checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}
case "$COMMAND" in
  build|archive)
    [[ $# == 1 ]] || { echo "expected one bin-os-arch target" >&2; exit 2; }
    parse_target "$1"
    if [[ "$COMMAND" == build ]]; then
      mkdir -p build
      (cd "cmd/$bin" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
        -o "$ROOT/build/$bin-$os-$arch$ext" .)
    else
      mkdir -p dist
      stage="$(mktemp -d)"
      trap 'rm -rf "$stage"' EXIT
      mkdir "$stage/$name"
      cp "build/$bin-$os-$arch$ext" "$stage/$name/$bin$ext"
      cp LICENSE README.md "$stage/$name/"
      if [[ "$os" == windows ]]; then
        (cd "$stage" && zip -qr "$name.zip" "$name")
        mv "$stage/$name.zip" "dist/$name.zip"
      else
        tar -C "$stage" -czf "dist/$name.tar.gz" "$name"
      fi
    fi
    ;;
  checksums)
    [[ $# -gt 0 ]] || { echo "expected release targets" >&2; exit 2; }
    archives=()
    for target in "$@"; do parse_target "$target"; archives+=("$name.$suffix"); done
    cd dist
    sums="$(checksum "${archives[@]}")"
    printf '%s\n' "$sums" | LC_ALL=C sort > SHA256SUMS
    ;;
  verify) cd dist; checksum -c SHA256SUMS ;;
  *) echo "usage: $0 build|archive TARGET | checksums TARGET... | verify" >&2; exit 2 ;;
esac
