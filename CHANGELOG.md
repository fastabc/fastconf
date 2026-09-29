# Changelog

User-visible release changes are recorded here. See the
[v1 migration guide](docs/cookbook/migration-v1.md) for v0 → v1 API changes
and the [v0 migration guide](docs/cookbook/migration-v0.md) for older releases.

## [Unreleased] — v1.0.0

### Changed

- Kept Prometheus and OTel in separate leaf modules with their original import
  paths. Logging adapters remain independent; their dependency-free shared
  implementation is now root-versioned (eleven modules overall).
- Modules release together at one version and are discovered from `go.mod`;
  removed the manual inventory and changed-only/force/delete release modes.
  Pinned upstream `gorelease` replaces the custom API snapshot generator.
- Subscriber callbacks run in registration order. Vault rejects empty tokens
  even with unrelated options; S3 size errors wrap `ErrConfigTooLarge`.
  Custom overlay parent directories are watched, including metadata updates.
  Render HTTP hooks have a 20-second timeout and do not follow redirects.
- Documented unsupported defaults/validation on collection elements; removed
  unused traversal branches. Removed provider build tags and dotenv's copied
  replacer exports (use `providers/env`); shared provider helpers and codec
  lookup now have one implementation.
- Moved NATS and Redis Streams providers to examples; removed the SDK-free
  OpenFeature imitation. Sidecar routes remain unchanged.
- Removed `source.HTTPSource` / `source.NewHTTP`; `providers/http` now supports
  Content-Type codec selection, revision snapshots and disabling polling.
- Commit and plan share pipeline execution; tracing and stage events share
  timing code. README files are the single user-manual source.

- Root source files and tests are organized by runtime responsibility. Shared
  initialization and direct callback functions reduce internal wrappers;
  public migrations are listed below and in the migration guide.
- The root module requires Go 1.24. Independent modules retain their own Go
  floors, tested separately in CI.
- Providers use `Load(ctx) (contracts.Snapshot, error)` and `Watch(ctx, from)`;
  optional `Describe()` supplies priority and watch paths. S3 and S3 Events
  implement this contract and require the matching root candidate.
- `Profile`, `Axis`, `Watch`, variadic provider/generator/transform/validator
  options, direct `Plan`, `History`, and `Pause/Resume` form the v1 API.
  Removed v0 names and replacements are listed in the migration guide.
- `Observer` unifies lifecycle notifications, metrics, audit and change feeds.
  Callbacks are synchronous unless wrapped in caller-owned `observe.Async`.
  Deadlines cancel callback contexts; callbacks must cooperate.
- `State.Map`, `Dump`, `Diff`, `Explain`, plan diffs and sidecar views mask
  secrets by default. Explicit plaintext views use `Unredacted`; the sidecar
  additionally requires its configured token.
- Struct-tag defaults always run. Unknown fields warn by default, with
  `UnknownError` and `UnknownIgnore` alternatives. Transforms include schema
  migrations; `WithTenant` labels a manager without a framework registry.
- File-layer caching and input fingerprints reduce unchanged reload work.
  RFC 6902 patches operate directly on copied maps. Snapshot diagnostics
  lazily retain a JSON tree while returned views remain detached.

- `PolicyError` is now defined in the root package, keeping its fields and
  error matching behavior while removing policy dependencies from contracts.
- Removed `SourceRef.Band`; source diagnostics retain `Kind`, `Priority`,
  `Profile` and `Path` without exposing internal priority-band names.

### Fixed

- Removed `transform.MergeByKey`, which could not merge across source layers;
  use `WithMergeKeys` for keyed list merging during assembly.
- Negative `WithHistory` capacities now return an option error from `New`
  instead of silently disabling history.

- Axis priority takes precedence over file count: higher-priority axes always
  win, and excess axes or files cannot cross into another priority band.
- Secret provenance uses dotted numeric list paths, matching `Explain` and
  secret-path patterns.
- HTTP document providers reject redirects by default so custom credential
  headers cannot be forwarded to another server.
- Release selection orders stable versions above their prereleases, ignores
  documentation-only module changes, and checks unpublished root dependencies.
- Historical provenance values cannot expose tagged or pattern-matched secrets
  inside lists, or secrets removed from the final configuration.
- `State.Cause` returns a detached revisions map.
- Shutdown cancels in-flight manual reloads and plans, including cooperative
  provider calls. Cancellation at stage boundaries prevents publication.
- Unchanged files still run custom JSON/YAML/text encoding hooks in `T`;
  the input fingerprint no longer skips dynamic decode results.
- Codec registry changes during assembly no longer attach the new generation
  to an older decoded result, so the next reload cannot be skipped incorrectly.
- JSON Patch rejects null documents/paths, invalid pointer escapes, signed
  array indices and operations beneath null parents without changing input.
- S3 snapshots preserve revisions and detach cached nested maps; S3 Events
  reports a resume gap on the first event after a non-empty resume request.
- `observe.Async` safely handles concurrent `Observe` and `Close`.
- Release candidate tags publish GitHub prereleases without replacing the
  latest stable release. Runtime, metadata and release documentation now
  describe the current API and module layout.
- Typed hooks support recursive types, embedded structs, pointers, slices and
  maps, follow the field selection of the configured decoder, and work with
  `WithDecoder(YAML)`. `DurationHook` now returns a `time.Duration`;
  `URLHook` fills `url.URL` / `*url.URL` fields.
- Provider and generator priorities order layers only within their class:
  files < generators < providers < `WithOverride`, whatever the values.
- The file watcher follows every scanned directory even when a reload does not
  publish, and re-registers directories that are removed and recreated.
- `render.Wire` serializes rendering with reloads so an older snapshot never
  overwrites a newer one, retries after a failed write and creates the file for
  an empty first render.
- Vault Watch re-announces a version until a Load reads it successfully.
- `transform.CurrentVersion` reads integer `json.Number` schema versions.
- The zerolog and phuslu slog adapters keep attributes in the group they were
  added to.
- Routing labels reject list indexes above `labels.MaxRoutingIndex` with a
  Load error instead of panicking.
- fastconfgen no longer emits duplicate field or type names when a generated
  suffix collides with an input name.

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

See [`docs/cookbook/migration-v0.md`](docs/cookbook/migration-v0.md) for full
examples and migration recipes.

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
