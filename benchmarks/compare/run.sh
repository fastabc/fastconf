#!/usr/bin/env bash
# Run on an otherwise idle machine; do not overlap these with race/build jobs.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT/benchmarks/compare"
OUT="${1:-results/local}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
export GOWORK=off
export GOMAXPROCS="${GOMAXPROCS:-4}"
export GOGC="${GOGC:-100}"
COUNT="${COUNT:-10}"
BENCHTIME="${BENCHTIME:-1s}"
SAMPLES="${SAMPLES:-100}"
{
  date -u '+%Y-%m-%dT%H:%M:%SZ'
  uname -smr
  go version
  go env GOOS GOARCH
  git -C "$ROOT" rev-parse HEAD
  git -C "$ROOT" diff --stat
  printf 'GOMAXPROCS=%s GOGC=%s count=%s benchtime=%s samples=%s\n' "$GOMAXPROCS" "$GOGC" "$COUNT" "$BENCHTIME" "$SAMPLES"
  if [[ "$(uname)" == Darwin ]]; then sysctl -n machdep.cpu.brand_string; else lscpu; fi
} > "$OUT/environment.txt"
python3 - "$ROOT" "$OUT/source-sha256.txt" <<'PYTHON'
from pathlib import Path
import hashlib
import subprocess
import sys
root = Path(sys.argv[1])
files = subprocess.check_output(["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"]).decode().split("\0")
with open(sys.argv[2], "w") as output:
    for name in sorted(set(files)):
        p = root / name
        if name and p.is_file() and not name.startswith("benchmarks/compare/results/"):
            output.write(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + name + "\n")
PYTHON
go list -m all > "$OUT/modules.txt"
go test -mod=readonly -run '^Test' -v ./... > "$OUT/correctness.txt"
go test -mod=readonly -run '^$' -bench . -benchmem -benchtime="$BENCHTIME" -count="$COUNT" > "$OUT/raw.txt"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
go build -mod=readonly -o "$TMP/sample" ./cmd/sample
for size in 4096 65536 1048576; do
  for adapter in FastConf Viper Koanf; do
    for mode in valid noop invalid; do
      name="$OUT/$size-$adapter-$mode"
      if [[ "$(uname)" == Darwin ]]; then
        /usr/bin/time -l "$TMP/sample" -adapter "$adapter" -size "$size" -mode "$mode" -samples "$SAMPLES" > "$name.json" 2> "$name.memory.txt"
      else
        /usr/bin/time -v "$TMP/sample" -adapter "$adapter" -size "$size" -mode "$mode" -samples "$SAMPLES" > "$name.json" 2> "$name.memory.txt"
      fi
    done
  done
done
# Pin benchstat independently; it is not a runtime dependency.
GOBIN="$TMP" go install golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e
(cd "$OUT" && "$TMP/benchstat" raw.txt) > "$OUT/summary.txt"
printf 'Comparison artifacts: %s\n' "$OUT"
