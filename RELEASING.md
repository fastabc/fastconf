# Releasing FastConf

FastConf keeps optional dependency stacks in separate Go modules, but releases
all modules together at one version. `go.mod` files are the source of truth:

```bash
bash tools/modules.sh list     # current modules
bash tools/modules.sh matrix   # CI matrix with each module's Go floor
```

There is no manually maintained module inventory. Root tags use `vX.Y.Z`;
modules below the root use `<directory>/vX.Y.Z`. Prometheus and OpenTelemetry
remain separate modules at `observability/metrics/prometheus` and
`observability/otel`. The stdlib-only logging core is part of the root module;
`integrations/log/phuslu` and `integrations/log/zerolog` remain optional modules.
`providers/s3/s3events` shares the S3 module's version. Pflag and playground
remain separate to keep their dependencies and Go floors out of the root graph.

## Prepare and verify

1. Update the changelog and migration guide in the release PR.
2. Align every in-repository `require` to the candidate version (currently
   `v1.0.0`). Do not commit local `replace` directives or synthetic checksums.
3. Run the checks below, then merge the reviewed implementation before tagging.

```bash
make check                              # release-script regression tests
make test-workspace                     # all modules against this checkout
make test-candidate VERSION=v1.0.0       # isolated module-protocol consumer tests
make api-check VERSION=v1.0.0 BASE=none  # establish the first stable API
make vulnerabilities                    # after dependencies are published
```

Before publication, run the vulnerability scan through the candidate proxy:

```bash
VERSION=v1.0.0 bash tools/check-candidate.sh bash tools/check-vulnerabilities.sh
```

`tools/modules.sh` discovers modules, builds CI matrices, and runs tests.
`check-candidate.sh` serves the checkout at `VERSION` through a temporary file
proxy and tests with `GOWORK=off`, a fresh module cache, and no replacements.
It serves exact candidate versions only; `latest` and version lists resolve
through the upstream proxy so API comparisons use a published baseline.
It fills candidate checksums and creates a clean temporary Git commit only in
its disposable checkout (gorelease requires a clean revision). This validates
unpublished cross-module requirements without changing source manifests.
To test a single candidate module:

```bash
bash tools/check-candidate.sh bash tools/modules.sh test providers/s3
```

`make test-all` checks already-published dependencies with `GOWORK=off`; it is
expected to fail while the declared root candidate is unpublished. After the
release, run it and commit any real downloaded checksums as normal maintenance.
Personal `go.work` files do not affect these checks.

## Public API and semantic versioning

API checks use pinned upstream [gorelease](https://pkg.go.dev/golang.org/x/exp/cmd/gorelease)
for every discovered module. It validates consumer dependency resolution and
compares exported APIs, including subpackages; there is no generated API snapshot.

The v0 to v1 redesign establishes a new stable API, so the first v1 check uses
`BASE=none` explicitly. This validates the candidate without claiming compatibility
with v0. Remove `BASE=none` from CI after v1 is published. Later releases use:

```bash
make api-check VERSION=v1.0.1              # compares with latest published version
make api-check VERSION=v1.1.0 BASE=v1.0.0  # explicit comparison
```

Patch releases preserve the public API; compatible additions require a minor
release. Breaking changes after v1 require a new major module path and migration
notes. Behavioral compatibility still needs tests and review.

## Go compatibility and vulnerability gate

The root module requires Go 1.24. All satellite modules require at least Go 1.24;
their minimum rises when updated dependencies require it. Each module declares
its minimum in `go.mod`, and CI tests satellites at that floor
(`tools/modules.sh matrix`).

| Module | Floor | Reason |
|---|---|---|
| `cue` | 1.26.0 | `golang.org/x/net` v0.59.0 and `golang.org/x/text` v0.42.0 |
| `integrations/log/zerolog` | 1.26.0 | `golang.org/x/sys` v0.48.0 |
| `observability/metrics/prometheus`, `observability/otel` | 1.26.0 | Updated `golang.org/x/*` dependencies |
| `policy/opa` | 1.26.0 | `github.com/open-policy-agent/opa` v1.21.0 |
| `validate/playground` | 1.26.0 | `github.com/go-playground/validator/v10` v10.30.5 |
| root and other modules | 1.24.0 | Root dependencies stay on their latest Go 1.24-compatible versions |

Run `make vulnerabilities` before tagging. The scan must report no reachable
vulnerabilities in any module. Record the toolchain, database date and target
platform; module/package-only findings are distinct from reachable calls.

The 2026-09-29 dependency update was scanned with `govulncheck v1.8.0`,
Go 1.26.6, and the database updated at 2026-09-28 16:43:40 UTC. All modules on
darwin/arm64 and the root module on windows/amd64 had zero reachable findings.
Two advisory IDs remain outside reachable calls:

- [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024): the root retains
  `golang.org/x/sys v0.41.0` for Go 1.24 compatibility. The fix in v0.44.0
  requires Go 1.25; the Windows scan imports the affected package but does not
  call `NewNTUnicodeString`.
- [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932): OPA and playground
  require `golang.org/x/crypto v0.57.0`, but do not import its affected OpenPGP
  packages. The advisory has no fixed version.

## Tagging

Release approval is still required before publishing. Preview the complete set:

```bash
bash tools/tag-release.sh v1.0.0 --dry-run
```

After the release commit is merged and approved:

```bash
bash tools/tag-release.sh v1.0.0 --push
```

The script validates all in-repository version pins before writing tags, creates
all module tags at the same commit, and pushes them atomically. It requires a
clean checkout for tag creation. Existing tags at the same commit are idempotent;
tags at another commit are rejected. There are no changed-only, retag, force, or
delete modes. Correct a published mistake with a new version.

Only the root tag triggers `.github/workflows/release.yml`; module tags do not
trigger duplicate binary releases. No tags are created by test or API checks.

## Pre-built binaries

Every root-module release (`v*` tag) auto-publishes 15 archives + a
SHA256SUMS file to the matching GitHub Release via
`.github/workflows/release.yml`. The matrix is:

| Binary | linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64 | windows/amd64 |
|---|:--:|:--:|:--:|:--:|:--:|
| `fastconfd` | tar.gz | tar.gz | tar.gz | tar.gz | zip |
| `fastconfctl` | tar.gz | tar.gz | tar.gz | tar.gz | zip |
| `fastconfgen` | tar.gz | tar.gz | tar.gz | tar.gz | zip |

Each archive contains the binary + LICENSE + README.md. The binaries are
pure-Go (CGO_ENABLED=0) and built with `-trimpath -ldflags "-s -w -X
main.version=<tag>"`; running `<bin> version` (or `<bin> -version` for
fastconfgen) prints the embedded tag.

To reproduce locally:

```bash
make dist VERSION=v1.0.0       # produce dist/*.tar.gz + dist/*.zip + dist/SHA256SUMS
make dist-verify               # verify checksums
make dist-clean                # rm -rf build dist
```

To extend the matrix without editing the Makefile:

```bash
make dist EXTRA_TARGETS="freebsd/amd64 linux/386"
```
