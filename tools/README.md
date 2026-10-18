# Repository tooling

Run `make` targets from the repository root. Scripts resolve the root from their
own location and support Bash 3.2 or newer. Checks use `GOWORK=off` and read-only
dependencies; workspace tests create and remove their own temporary workspace.
No command publishes a release unless `tag-release.sh --push` is explicitly used.
Pushing a root version tag starts the release workflow.

## Development

| Command | Scope |
| --- | --- |
| `make build` / `make test` | Root plus fastconfgen builds / root-module race tests |
| `make test-all` | Every module against its declared dependencies |
| `make test-workspace` | Every module against the local checkout |
| `make test-candidate` | Every module through an isolated candidate proxy/cache |
| `make check` | Formatting, dependency boundaries, tidy metadata and tooling regression tests |
| `make lint` | Root module; requires golangci-lint v2 |
| `make tools-check` | Requires shellcheck and actionlint |
| `make bench` / `make bench-guard` | Benchmarks / hot-read budget |
| `make cover` / `make example` | Root coverage / godoc, scenario tests and optional S3/pflag programs |
| `make soak` | Resource soak test; `FASTCONF_SOAK_DURATION=5m` for an extended run |
| `make tidy` | Update dependency metadata in the root and all submodules, including benchmarks |
| `make tidy-check` | Run `go mod tidy -diff` for the same modules; fail on drift without editing files |
| `make module-matrix` | CI JSON from discovered submodule Go floors |

Use `MODULES=". cue"` with `test-all`, `test-workspace`, `test-candidate`, `tidy` or `tidy-check` to
select modules. `bash tools/modules.sh exec COMMAND ...` runs a command from the
repository root in a temporary workspace containing all modules. Module inventory
comes from tracked and unignored `go.mod` files, excluding benchmark-only modules
under `benchmarks/`, not a maintained list. Inventory
and release tools require a Git checkout; `make test` also works in source archives.

Tidy commands include benchmark modules and use each module's declared dependencies
with `GOWORK=off`. They preserve existing dependency constraints; let Go decide which
older checksums are still needed instead of deleting entries by version.
`make check` includes `tidy-check`, so CI and release checks reject untidy metadata.
Use Go 1.26+ for the full inventory; checks may download dependencies but do not
rewrite `go.mod` or `go.sum`.

`bash tools/profile-reload.sh OUTPUT_DIRECTORY` records benchmarks and profiles.
`MAX_NS_PER_OP`, `MAX_ALLOCS` and `BENCH_TIME` configure the benchmark guard.

## Release validation

`VERSION` defaults to `v1.1.0`. Candidate checks copy current tracked and unignored
files into a disposable repository and serve that version through a local Go
module proxy. Dependencies and generated sums stay in the disposable copy.
Only checksum entries for discovered modules at the candidate version are
regenerated in that copy and its proxy archives. Third-party checksums, other
versions and the source checkout's `go.sum` files remain unchanged. This checks
local candidate content; it does not replace a version cached by public Go proxies.

- `make test-candidate VERSION=vX.Y.Z`: test candidate modules.
- `make api-check VERSION=v1.2.0 BASE=v1.1.0`: compare the candidate to published APIs.
  Use `BASE=none` only when intentionally establishing a new stable API baseline;
  v1.1.0 does so because public v1.0.0 proxy content conflicts with its immutable
  checksum record. Subsequent checks use `BASE=v1.1.0`.
- `make vulnerabilities`: scan declared dependencies with pinned govulncheck.
- `bash tools/check-candidate.sh make vulnerabilities`: scan candidate dependencies.
- `bash tools/tag-release.sh vX.Y.Z --dry-run`: validate and list coordinated tags.
- `bash tools/tag-release.sh vX.Y.Z [--push]`: create immutable annotated tags;
  `--push` sends satellite tags atomically to origin, then sends the root tag
  alone to trigger the release workflow. A failed second push can be retried.
  Requires a clean checkout; see [release ordering](../RELEASING.md#tagging).

## Distribution

`make dist VERSION=vX.Y.Z` builds fastconfd, fastconfctl and fastconfgen for Linux
and macOS (amd64/arm64) and Windows (amd64). It requires Go, tar and zip; checksums
use sha256sum or shasum. Builds disable CGO and embed the version.

Use `TARGETS="linux/amd64"` to replace the platform matrix or `EXTRA_TARGETS` to
extend it. `make dist-fastconfgen` archives one binary; `make
build/fastconfgen-linux-amd64` builds one target; `make
dist/fastconfgen-linux-amd64` archives it. `make dist-verify` verifies the manifest
from the last full `make dist`; only that invocation's matrix enters SHA256SUMS.
`make dist-clean` removes build and dist outputs.

CI reuses these Make targets, tests each submodule at its declared minimum Go
version and runs the root module on Linux, macOS and Windows. The release workflow
verifies that every module tag exists at the same commit, tests candidates, then
builds and publishes archives. Resource soak remains a separate manual workflow.
The v1.1.0 release used `BASE=none` to establish its baseline because public v1.0.0
proxy content conflicts with its immutable checksum record. Subsequent release
checks compare against `BASE=v1.1.0`.

CI compares the next candidate (`VERSION=v1.2.0`) against `BASE=v1.1.0`.
The candidate must be newer than the baseline; this check does not create a release.
Baseline download failures fail the check; never replace them with `BASE=none`.

`bash tools/check-api-negative.sh` verifies the real gorelease gate rejects
removed root and pflag exports in disposable copies. It needs network access
to the fixed v1.1.0 baseline and does not change the working tree.

The isolated [comparison project](../benchmarks/compare/README.md) pins competitor
dependencies outside the runtime/release modules. Run its tests separately.
