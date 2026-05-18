# Releasing FastConf

FastConf publishes Go modules from a single repository:

| Module path | Releases independently? | Tag prefix |
|---|:--:|---|
| `github.com/fastabc/fastconf` | yes (root) | `vX.Y.Z` |
| `github.com/fastabc/fastconf/cue` | yes | `cue/vX.Y.Z` |
| `github.com/fastabc/fastconf/integrations/cli/pflag` | yes | `integrations/cli/pflag/vX.Y.Z` |
| `github.com/fastabc/fastconf/integrations/log/phuslu` | yes | `integrations/log/phuslu/vX.Y.Z` |
| `github.com/fastabc/fastconf/integrations/log/zerolog` | yes | `integrations/log/zerolog/vX.Y.Z` |
| `github.com/fastabc/fastconf/observability/metrics/prometheus` | yes | `observability/metrics/prometheus/vX.Y.Z` |
| `github.com/fastabc/fastconf/observability/otel` | yes | `observability/otel/vX.Y.Z` |
| `github.com/fastabc/fastconf/policy/opa` | yes | `policy/opa/vX.Y.Z` |
| `github.com/fastabc/fastconf/providers/s3` | yes | `providers/s3/vX.Y.Z` |
| `github.com/fastabc/fastconf/validate/playground` | yes (v0.9.0+) | `validate/playground/vX.Y.Z` |

Subpackages without their own `go.mod` inherit the root module's version.
The following packages are root-versioned (no independent `go.mod`):
`cmd/fastconfctl`, `cmd/fastconfgen`, `cmd/fastconfd`,
`integrations/bus`, `integrations/openfeature`, `integrations/render`,
`contracts`, `providers/http`, `providers/nats`, `providers/redisstream`,
`providers/vault`, `providers/consul`, `providers/k8s`,
`providers/s3/s3events`, `pkg/*`, `policy/` (root).

> **Note on legacy tags (v0.9 and earlier)**: Prior to v0.10.0 the observability
> sub-modules lived directly under `metrics/prometheus/` and `otel/`.
> v0.10.0 moves them under `observability/metrics/prometheus/` and
> `observability/otel/` and uses new tag prefixes accordingly. Old tags remain
> in the repository for historical reference and can be safely ignored.

## Semantic versioning

- **Patch** (`v0.x.y` → `v0.x.(y+1)`): bug fixes, doc updates, internal
  refactors that do not change the public surface.
- **Minor** (`v0.x.0` → `v0.(x+1).0`): backwards-compatible additions to the
  public packages listed in `CHANGELOG.md`.
- **Major** (`vX.0.0` → `v(X+1).0.0`): breaking changes to anything in the
  public surface. Every breaking change MUST appear in `CHANGELOG.md` with a
  migration note.

`internal/...` is exempt — it may change in a patch release.

## Branching & PR rules

1. All work lands on `main` via reviewed PRs (CODEOWNERS auto-assigns).
2. CI must be green: `go test -race ./...` for the root module **and**
   each independent subpackage module, plus `golangci-lint run`.
3. Documentation changes that ship with code go in the same PR.
4. Release PRs only update `CHANGELOG.md` (move `[Unreleased]` to a dated
   version) and bump example go.mod requires when needed.

## Tagging procedure

**Preferred: use the unified script** — tags all 10 modules in one command
(1 root + 9 sub-modules):

```bash
git switch main && git pull
# Update CHANGELOG.md, commit
./tools/tag-release.sh v0.x.y --push

# To overwrite existing tags (retag):
./tools/tag-release.sh v0.x.y --force --push

# To remove an accidental release tag set:
./tools/tag-release.sh v0.x.y --delete --push
```

Pushing the root tag (`vX.Y.Z`) triggers
`.github/workflows/release.yml`, which cross-compiles `fastconfd`,
`fastconfctl`, and `fastconfgen` across 5 OS+arch targets and uploads
15 archives + `SHA256SUMS` to the matching GitHub Release. Sub-module
path-prefixed tags do not match `v*` and therefore do not double-fire
the release workflow.

**Manual (individual modules)** — for the root module:

```bash
git tag -a v0.x.y -m "fastconf v0.x.y"
git push origin v0.x.y
```

For a subpackage module (path-prefixed tag is required by the Go toolchain):

```bash
git tag -a observability/metrics/prometheus/v0.x.y \
    -m "observability/metrics/prometheus v0.x.y"
git push origin observability/metrics/prometheus/v0.x.y
```

The same pattern applies to every independent module listed in the table.

## Pre-built binaries (v0.9.0+)

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
make dist VERSION=v0.x.y       # produce dist/*.tar.gz + dist/*.zip + dist/SHA256SUMS
make dist-verify               # verify checksums
make dist-clean                # rm -rf build dist
```

To extend the matrix without editing the Makefile:

```bash
make dist EXTRA_TARGETS="freebsd/amd64 linux/386"
```

## Public-API compatibility check

Until `v1.0.0` we do not run `golang.org/x/exp/cmd/apidiff` automatically.
Reviewers MUST eyeball every change to:

- `contracts/*.go`
- exported identifiers in top-level `*.go` (the main `package fastconf`)
- exported identifiers in any subpackage with its own `go.mod`

Any signature change requires either a `CHANGELOG.md` migration note or a
deprecation in place of the breaking change.
