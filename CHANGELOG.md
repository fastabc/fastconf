# Changelog

All notable changes to FastConf are documented here.

FastConf is currently **pre-public**: the API may change without notice
and there is no migration commitment. The changelog tracks the current
state of `main`, not a release history. Earlier iterations have been
collapsed into a single baseline — the project is the API described in
[`README.md`](./README.md), not the path that got there.

## [Unreleased]

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

## [v0.15.0] — 2026-05-16

First numbered pre-public release. Tracks the contract-polish wave that
collapses every "shape-before-semantics" rough edge surfaced in
`docs/plans/2026-05-16-project-evaluation.md` and lays the groundwork
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
