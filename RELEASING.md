# Releasing FastConf

FastConf v1.1.0 establishes the current stable API baseline. The public v1.0.0
proxy content conflicts with its immutable Go checksum record, so that version
cannot be corrected. Optional integrations live in separate Go modules; all
modules use the same release version. Discover modules and minimum Go versions
directly from their manifests:

```sh
bash tools/modules.sh list
bash tools/modules.sh matrix
```

Root tags use `vX.Y.Z`; satellite tags use `<directory>/vX.Y.Z`.
`providers/s3/s3events` shares the S3 module's version. Logging adapters,
Prometheus and OpenTelemetry retain separate leaf modules.

## Prepare and verify

The current release target is `v1.2.0`, checked against the stable `v1.1.0`
baseline. Prepare it with the following steps:

1. Update the changelog and any relevant migration notes.
2. Align in-repository module requirements and the Makefile `VERSION` default to
   the release version. Do not
   commit local `replace` directives or synthetic checksums in release modules.
   The standalone benchmark module is excluded from release inventory.
3. Run the checks against the final source, then merge the reviewed release
   commit before tagging.

```sh
make tidy VERSION=v1.2.0
make check VERSION=v1.2.0
make lint VERSION=v1.2.0
make test-workspace
make test-candidate VERSION=v1.2.0
make api-check VERSION=v1.2.0 BASE=v1.1.0
VERSION=v1.2.0 bash tools/check-candidate.sh bash tools/check-vulnerabilities.sh
```

Tidy uses the isolated candidate proxy for internal dependencies at `VERSION`,
so these checks work before tags are published. It checks `go.mod` and external
or older-version checksums, while excluding exact candidate-version checksums
from comparison. The write command never copies synthetic checksums to the
source checkout. After publication, ordinary `GOWORK=off go mod tidy` in each
module can add the real published checksums.

`test-workspace` tests all modules against local source. `test-candidate` serves
that source through a temporary file proxy with `GOWORK=off`, a fresh module
cache and no replacements. Candidate archives, checksums and temporary Git
commits remain in its disposable checkout. This verifies a source combination
without publishing it. To test one candidate module:

```sh
VERSION=v1.2.0 bash tools/check-candidate.sh bash tools/modules.sh test providers/s3
```

After publication, `make test-all` checks independent resolution of the published
module requirements. Commit downloaded checksums as normal maintenance. Personal
`go.work` files do not affect these independent tests.

## API compatibility

`api-check` uses pinned upstream
[gorelease](https://pkg.go.dev/golang.org/x/exp/cmd/gorelease) for every discovered
module and its subpackages. Patch releases preserve the public API; compatible
additions require a minor release. Breaking changes require a new major module
path and migration notes. Tests and review must also cover behavioral changes.
`BASE=none` skips compatibility comparison. The v1.1.0 release deliberately
establishes a new baseline because public v1.0.0 proxy content conflicts with its
immutable checksum record. Future checks use `BASE=v1.1.0` and a newer candidate, for example:

```sh
make api-check BASE=v1.1.0 VERSION=v1.2.0
```

CI uses that explicit comparison. A baseline download failure must fail the check;
do not fall back to `BASE=none`. API checks do not replace behavior regression tests.

Explicit baseline tags must be available locally (`git fetch --tags` for a shallow
checkout). Candidate checks retain that tag's tree. A module whose `go.mod` does
not exist in that verified baseline tree uses `-base=none` for its first release;
modules already present still require their published baseline to resolve.

## Go versions and vulnerabilities

The root module requires Go 1.24. Satellite modules declare their own minimums;
CI tests each at that floor. Use `tools/modules.sh matrix` rather than maintaining
a separate version table.

Run the vulnerability check before every release and record the toolchain,
database date and target platform. Require no reachable vulnerabilities in any
module; distinguish module/package-only findings from reachable calls. After
publication, `make vulnerabilities` scans independently resolved dependencies.
Historical scans do not validate a later source or dependency set.

## Tagging

Preview and publish the complete module tag set from a clean release commit:

```sh
bash tools/tag-release.sh v1.2.0 --dry-run
bash tools/tag-release.sh v1.2.0 --push
```

The script validates all version pins before creating tags at one commit. It
first pushes the satellite tags atomically, then pushes the root tag alone.
This ensures the release workflow starts only after satellite tags exist;
[GitHub suppresses tag push events when more than three tags are pushed at once](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#push).
If the root push fails, rerun the same command to finish; satellite tags may
already exist, and the two pushes are not one atomic transaction. Tags at the same commit are idempotent; an existing
tag at another commit is rejected, including in dry-run mode. There are no
force-retag or deletion modes. Go module versions are immutable: deleting a Git
release or tag does not erase cached archives or checksum records. Publish a
new version for changed content; see the official
[Go module publishing guidance](https://go.dev/blog/publishing-go-modules).

Only the root tag triggers `.github/workflows/release.yml`; satellite tags do
not create duplicate binary releases. Test and API checks never create tags.

## Binary distribution

Each root release builds `fastconfd`, `fastconfctl` and `fastconfgen` for
linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64: 15 archives
plus `SHA256SUMS`. Windows uses ZIP; other targets use tar.gz. Archives contain
the binary, LICENSE and README.md. Builds disable CGO, use `-trimpath`, and embed
the tag through `-X main.version=<tag>`.

```sh
make dist VERSION=v1.2.0
make dist-verify
```

Use `fastconfctl version` or `fastconfgen -version` to inspect those binaries.
The running daemon reports its version at `GET /version`; `fastconfd` has no
version subcommand. `make dist-clean` removes build artifacts.
Extend the matrix with `make dist EXTRA_TARGETS="freebsd/amd64 linux/386"`.
