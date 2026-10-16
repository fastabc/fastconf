# Changelog

The [migration guide](docs/cookbook/migration-v1.md) covers upgrading from
v0.20.0 to v1.1.0. Older entries below describe their historical releases.

## [v1.1.0]

This release establishes a new stable API baseline. The previously published
v1.0.0 Go module version remains unchanged; its immutable checksum record cannot
be rewritten.

### Added

- S3 `WithMaxBodyBytes` for configurable object limits; the default remains
  10 MiB. Both constructors accept options, with non-positive limits restoring
  the default. See the [S3 changelog](providers/s3/CHANGELOG.md).
- Built-in INI input for directory layers and document providers, with literal
  section/key names, string values and existing typed-hook conversion.
- Runnable `examples/ini` and a recipe covering syntax and strict mixed-format merging.
- Runnable basic, local multi-tenant, reload/Plan/rollback and feature rollout
  examples with complete configuration and scenario tests.
- Direct Plan, History and Pause/Resume APIs; application-owned tenant managers
  labeled with `WithTenant`, and function-based transforms and validators.
- A unified Observer interface for reload/stage events, metrics, audit and
  commit diffs, with caller-owned asynchronous delivery through `observe.Async`.

### Changed

- `.ini` files in configuration directories now participate in loading. Invalid
  INI fails the load; failed reloads retain the previous snapshot. INI support
  does not add an output format or `ini` struct tags.
- Stable constructor and type names across providers; document decoding uses
  `contracts.Decoder`, struct decoding uses `WithDecodeFormat`.
- Providers return `contracts.Snapshot`; native notifications use optional
  `contracts.Watcher`, and optional `Describe` supplies priority/watch paths.
- Snapshot diagnostics, Plan/commit diffs and sidecar output mask secrets by
  default. Plaintext views require explicit opt-in.
- Event timestamps use `At time.Time`; HTTP/SSE timestamps use RFC 3339 strings.
- Struct defaults always apply, unknown fields warn by default, and negative
  history capacities fail construction.
- Root requires Go 1.24. Eleven discovered modules release at one version;
  optional integrations retain their own dependency and toolchain requirements.
- NATS/Redis Streams moved to examples. HTTP documents use `providers/http`;
  the unused bus and OpenFeature imitation were removed.

### Fixed

- YAML bridge hashes now use YAML encoding of the final typed value, so changes
  to fields tagged `json:"-"` publish snapshots and notify subscribers. JSON
  diagnostic views still omit those fields; custom marshalers must be deterministic.
- Consul KV queries respect directory boundaries, and both KV and Blob decoding
  reject sibling prefixes while Blob preserves exact-key documents.
- `render.HTTPGet` documentation now matches its 10-second request timeout.
- Axis/source precedence, keyed-list merging, file-watch refresh and source
  ownership now preserve deterministic layer ordering and isolated snapshots.
- Reload cancellation, shutdown and failed validation preserve the last good
  snapshot; bounded queues prevent slow consumers from blocking error delivery.
- Secret provenance masks historical/container values; JSON Patch rejects invalid
  operations without mutating input. HTTP readers reject redirects by default
  and bound document sizes.
- Typed hooks handle recursive/container fields and URL assignment. File caches
  respect user hooks and codec-registry changes; read paths remain allocation-free.
- Render serialization, async observer shutdown, routing index bounds and
  generated Go identifier collisions are covered by regression tests.

## [v0.18.0] — 2026-05-19

First public release.

### Breaking changes (import path migration)

Three sub-module paths have changed. Update your `go.mod` and import statements:

| Old import path | New import path |
|---|---|
| `github.com/fastabc/fastconf/policy/cue` | `github.com/fastabc/fastconf/cue/policy` |
| `github.com/fastabc/fastconf/validate/cue/cuelang` | `github.com/fastabc/fastconf/cue/cuelang` |
| `github.com/fastabc/fastconf/providers/s3events` | `github.com/fastabc/fastconf/providers/s3/s3events` |

The `cue/` top-level module (`github.com/fastabc/fastconf/cue`) replaces the two former
CUE sub-modules (`policy/cue` and `validate/cue/cuelang`), merging them under a single
shared `cuelang.org/go` runtime. `providers/s3events` is now a subpackage of `providers/s3`
(same `go get github.com/fastabc/fastconf/providers/s3@latest` install).

### Breaking changes (API)

- **Bucketed options:** 11 flat `With*` setters replaced by
  `WithProfile(ProfileOptions{…})`, `WithWatch(WatchOptions{…})`, and
  `WithCoalesce(CoalesceOptions{…})`. The old names are deleted.
- **`WithDefaulterFunc` → `WithDefaults`.**
- **`Sub` → `Extract`.**
- **`State[T].Diff` now returns `[]DiffEntry`.** Use
  `fastconf.FormatDiff(entries)` to get the previous `[]string` line list.
- **`provider.NewCLIChanged` removed.** Use `provider.NewCLI`.
- **`OverlayAxis`, `Transformer`, `MigrationApplier`, `MigrationFunc`,
  `CodecBridge` are now root-native types.** Field names are
  unchanged; existing struct literals compile without modification.

## [v0.15.0] — 2026-05-16

First numbered pre-public release.

### Reload pipeline

- **`Reload(ctx)` now threads the caller's ctx through the running
  pipeline.** A timeout / cancellation actually aborts slow
  `provider.Load`, secret resolvers, and transformers — `ctx.Err()` is
  returned raw (no `ErrDecode` wrapping) so `errors.Is(err,
  context.DeadlineExceeded)` works. fsnotify / provider-watcher
  triggers continue to use `context.Background()`.
- Failure-safe contract unchanged: a cancelled reload preserves the
  previous `*State[T]`, does not advance `Generation`, and publishes
  the ctx error on `Errors()`.

### DiffReporter backpressure

- Per-reporter bounded queue + dedicated worker goroutine replaces the
  prior unbounded `go func()` fan-out. Queue-full → drop +
  `MetricsSink.EventDropped("diff-reporter:<idx>")`.
- New `WithDiffReporterQueueCap(n int)` Option (default 64).
- New optional `DiffReporterMetricsSink` extension interface —
  framework samples `(depth, capacity)` after every enqueue so a
  Prometheus gauge can show how close each reporter is to its drop
  threshold.

### Provider-by-name registry

- New `*ProviderRegistry` type + `NewProviderRegistry()` constructor +
  `WithProviderRegistry(r)` Option. Lookup order: Manager-local →
  process-wide default. Lets multi-tenant tests and sub-systems
  isolate factories without mutating global state.
- `WithProviderByName` resolution is now deferred to after every
  Option has applied, so `WithProviderRegistry` may appear in any
  order relative to it. Existing zero-config `RegisterProviderFactory`
  callers are unaffected.

### State / API hygiene

- `State.MarshalYAML(redactor)` honours the redactor — secret-marked
  fields are properly masked in the YAML output when a non-nil
  redactor is supplied. (Previously the parameter was reserved /
  ignored.)
- `State.Redacted` / `Origins` / `Explain` / `Lookup` now tolerate a
  nil receiver the same way `Diff` / `MarshalYAML` / `Introspect`
  already did. Pinned by `TestState_NilSafety`.

### Provider defaults

- `providers/consul`: no longer uses `http.DefaultClient` (blocking
  queries up to 5 m are incompatible with a shared global client).
  Default client is an isolated `&http.Client{}` governed by ctx;
  configure transport-level limits via `WithClient` when needed.
- `providers/vault` (`AppRoleAuth` fallback): same treatment — replaces
  `http.DefaultClient` with an isolated `&http.Client{Timeout: 10s}`
  matching the main vault default. New cookbook page:
  `docs/cookbook/provider-timeouts.md`.
