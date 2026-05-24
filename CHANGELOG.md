# Changelog

All notable changes to FastConf are documented here.
See [`docs/cookbook/migration-v0.18.md`](docs/cookbook/migration-v0.18.md)
for the step-by-step migration guide.

## [Unreleased]

### Breaking changes (API)

- **`Subscribe` is now diff-aware by default.** The callback fires only
  when the value extracted by `extract` actually changes between two
  consecutive reloads. Equality is determined by `reflect.DeepEqual` on
  the dereferenced values. Two-nil transitions do not fire; nil ↔
  non-nil transitions always fire.

  ```go
  // v0.18: callback ran on every reload, you wrote the filter yourself
  fastconf.Subscribe(mgr, extract, func(old, new *T) {
      if old != nil && *old == *new { return }
      reconnect(new)
  })

  // v0.19: framework owns the diff
  fastconf.Subscribe(mgr, extract, func(old, new *T) {
      reconnect(new) // guaranteed: value actually changed
  })
  ```

  **Quiet hazard.** A v0.18 subscriber whose side effect ran on every
  reload (audit, mirror, heartbeat) without a caller-side filter will
  see fewer invocations after upgrading. To restore the v0.18 semantics,
  pass `WithEqual(func(_, _ *T) bool { return false })`.

### Added

- **`WithEqual[M any](equal func(old, new *M) bool) SubscribeOption[M]`** —
  replaces the default `reflect.DeepEqual` comparator with a custom
  function. Used to ignore noisy fields, hash-compare large structs, or
  restore the fire-on-every-reload idiom.

See [`docs/cookbook/migration-v0.19.md`](docs/cookbook/migration-v0.19.md)
for focused examples.

### Migration

| v0.18 call                                                | v0.19 replacement                                                       |
| --------------------------------------------------------- | ----------------------------------------------------------------------- |
| `Subscribe(m, ex, fn)` (callback expected every reload)   | `Subscribe(m, ex, fn, WithEqual(func(_,_ *T) bool { return false }))`   |
| `Subscribe(m, ex, fn)` with inline `*old == *new` filter  | `Subscribe(m, ex, fn)` — delete the filter                              |

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

See [`docs/cookbook/migration-v0.18.md`](docs/cookbook/migration-v0.18.md) for full
examples and migration recipes.

- **Bucketed options (SPEC-A1):** 11 flat `With*` setters replaced by
  `WithProfile(ProfileOptions{…})`, `WithWatch(WatchOptions{…})`, and
  `WithCoalesce(CoalesceOptions{…})`. The old names are deleted.
- **`WithDefaulterFunc` → `WithDefaults` (SPEC-A6).**
- **`Sub` → `Extract` (SPEC-A8).**
- **`State[T].Diff` now returns `[]DiffEntry` (SPEC-A4).** Use
  `fastconf.FormatDiff(entries)` to get the previous `[]string` line list.
- **`provider.NewCLIChanged` removed (SPEC-E2).** Use `provider.NewCLI`.
- **`OverlayAxis`, `Transformer`, `MigrationApplier`, `MigrationFunc`,
  `CodecBridge` are now root-native types (SPEC-A3).** Field names are
  unchanged; existing struct literals compile without modification.

### Architecture

- Moved `Manager[T]`, the reload pipeline, plan/replay/watch helpers, and
  receiver-method internals behind `internal/manager`.
- Moved option storage into `internal/options`, observability contracts into
  `internal/obs`, tenant registry into `internal/tenant`, and generic
  `State[T]` implementation into `internal/state`.
- Root package is now a 12-file public facade of type aliases,
  constructors, and `With*` wrappers. Public API signatures are unchanged.
- `go.mod` minimum version lowered to `go 1.22`; all language features in
  use (`generics`, `atomic.Pointer`) are available since Go 1.18/1.19.

### Compatibility

- `fastconf.Manager[T]`, `State[T]`, `Option`, `TenantManager[T]`,
  `Replay[T]`, `Watcher[T]`, `PlanResult[T]`, and observability interfaces
  remain available at the root package through aliases.
- `Manager.Get` remains zero allocation; bench guard reports 0.4339 ns/op,
  0 B/op, 0 allocs/op on the local arm64 baseline.

## [v0.15.0] — 2026-05-16

First numbered pre-public release. Tracks the contract-polish wave that
collapses every "shape-before-semantics" rough edge surfaced in
`docs/plans/archive/2026-05-16-project-evaluation.md` and lays the groundwork
for a 9+/10 publish-readiness score.

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

- `State.MarshalYAML(redactor)` honours the redactor — `fastconf:"secret"`
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

### Baseline reminder

Everything below the v0.15.0 line is the same baseline as
[Unreleased] v0.14: strongly-typed `*Manager[T]`, lock-free
`atomic.Pointer.Load` hot read, Kustomize-style overlay merge, opt-in
extension points, pre-public API (no migration commitments).

For full reference see `README.md` and `docs/design/`.
