# Changelog

The [migration guide](docs/cookbook/migration-v1.md) covers upgrading from
v0.20.0 to v1.1.0, from v1.2.0 to v1.3.0, from v1.3.0 to v1.4.0 and from
v1.4.0 to v1.5.0. Older entries below describe their historical releases.

## [Unreleased]

### Added

- `WithSecretResolvers` and `PrefixSecretResolver` for ordered decrypter chains.

- `contracts.Notifier`, `WithNotifier` and `WithResync` for manager-owned
  consumer supervision and periodic reconciliation.

- `transform.Interpolate` with environment defaults, escapes, required values and
  optional scalar configuration references with cycle/depth checks.

- Absent-key `default` struct tags and `LayerDefault` provenance, with literal
  validation during manager construction.

- `codec.WithListCoercion` for scalar lists and `codec.NamedPrimitiveHook` as a
  fallback after application hooks; `azappconfig.WithJSONValues` for JSON settings.

### Changed

- Resolver-produced plaintext is automatically masked in diagnostic `Map`, `Dump`,
  `Diff` and `Explain` views; `Get` and `Unredacted` retain plaintext access.

- The reload pipeline and stage metrics now include `defaults` after `secret`.

- Default typed hooks now accept comma-separated lists, JSON array strings,
  contiguous index maps and named numeric/boolean string inputs that previously
  failed decoding. Byte lists and custom unmarshalers keep their wire semantics.

## [v1.5.0]

**Go 1.25 is now required by the root module.** This is a minor release because
the minimum supported Go version increased from 1.24; the public API is unchanged.

### Security

- Update dependencies to clear vulnerabilities reported by govulncheck; FastConf
  code does not call the affected functions:
  - `golang.org/x/sys` v0.44.0 (GO-2026-5024) in the root module.
  - `golang.org/x/net` v0.62.0 (GO-2026-6860, GO-2026-6866) in `cue`, notify
    `azservicebus` and `pubsub`, and providers `azappconfig`, `azblob`, `gcs`
    and `secretmanager`.
  - `golang.org/x/crypto` v0.58.1-0.20261009181718-2f1f834330b2 (GO-2026-5932,
    deprecated `openpgp`; no tagged release yet) in notify `pubsub`, `policy/opa`,
    providers `gcs` and `secretmanager`, and `validate/playground`.
  - `github.com/klauspost/compress` v1.20.2 (GO-2026-6865) in `providers/azblob`.

### Changed

- The root module and the optional modules that depend on it (`integrations/cli/pflag`,
  `integrations/log/phuslu`, `integrations/notify/sns`, providers `appconfig`,
  `parameterstore` and `s3`) now require Go 1.25; the fixed `golang.org/x/sys`
  no longer supports Go 1.24. `cmd/fastconfgen` still requires Go 1.24.

## [v1.4.0]

### Changed

- v1.4.0 establishes a new API compatibility baseline; later releases are
  checked against it. Fields of the unexported option struct, which gorelease
  reaches through `Option`, were renamed. Applications cannot name these fields
  and need no source change.
- Internal file layout and identifier names now follow each component's role.
  Runtime behavior is unchanged.

## [v1.3.0]

### Added

- `contracts.ReadAllMax` and `contracts.SafeError` for custom providers. Old
  providerutil entry points and logging internals remain compatible with v1 modules.

### Fixed

- YAML fields named `inline` retain typed hooks; only tag options enable inlining.
- Axis values cannot traverse outside their overlay directory.
- Root metadata paths are normalized for virtual filesystems, including `WithDir(".")`.
- fc range and enumeration constraints validate pointed-to scalar values.
- File-triggered reloads retain one pending refresh per directory while busy.
- Consul changes remain pending until Load succeeds, including decoding failures.
- Dotenv path conflicts resolve in file and line order, with later entries winning.
- Expanded Kubernetes metadata resolves conflicting keys in lexical key order.
- `fastconfctl diff` starts from base only when `-from` is empty.
- fastconfd exits on listen failure and shuts down on unexpected serving errors.
- Secret maps named `inline` no longer mask unrelated provenance paths.
- String schema versions no longer cause already-applied migrations to run again.
- Playground validation errors omit field values, preventing secret disclosure.
- conf.d special files cannot block discovery; files and metadata are bounded at 4 MiB.
- Discovery errors wrap `ErrDecode`; oversized files retain `ErrTooLarge`.
- fastconfd help and invalid-argument usage hide environment token values.
- fc defaults and constraints apply to promoted fields from unexported embeds.
- Routing labels preserve equals signs inside map-form keys.
- s3events logs one sanitized warning per receive outage, resetting on recovery.
- API checks distinguish candidate dependency pins from the intended minor release;
  cross-module dependency checks also inspect untracked Go files.

### Changed

- fc `min`, `max` and `oneof` dereference non-nil pointers and skip nil pointers;
  `required` continues to check the pointer itself for nil.
- An empty diff `-from` ignores environment and metadata profile defaults.
- `transform.CurrentVersion` accepts decimal integer strings with surrounding
  whitespace, all built-in signed/unsigned integer widths and integral floats.
- Playground validation messages retain field names and rules without `got=` values.
- Reflection plans share field/type primitives; tests use compiled production paths.
- Provider retry backoff and fastconfctl dump loading share one implementation each.
- Root providers use the public bounded-read helper; old internal entry points
  remain compatibility shims. Routing normalizes copied label pairs in place.

### Deprecated

- `providers/s3/s3events`: use `integrations/notify/sns` for refresh-before-ack delivery.

## [v1.2.0]

### Added

- `LoadFile[T]` loads one required file (up to 4 MiB) with unknown fields rejected.
- `source.NewFS` loads a required document from `fs.FS`, including embedded files.
- Independent AWS Parameter Store, GCS and Azure Blob provider modules accept
  application-owned SDK clients; cloud SDKs stay outside the root module.
  Parameter Store expands recursive paths; GCS/Azure cache decoded versions.
- Independent AppConfig, Azure App Configuration and GCP Secret Manager loaders
  add deployed-session, prefix/label and version/CRC32C-aware document loading.
- Independent SNS-over-SQS, Azure Service Bus and Pub/Sub notification consumers
  wait for successful refresh before acknowledgement. Examples document dedicated
  replica subscriptions, retry/dead-letter policies and periodic reconciliation.

### Changed

- `fastconfctl dump`, `diff` and `explain` mask common credential keys by default;
  pass `-unredacted` for plaintext. Diff now reuses snapshot diagnostics.
- File caches retain only the latest decoded version per path/codec. Unchanged
  file reloads avoid cloning; snapshots share compiled redaction metadata and
  lazily cache diagnostic trees. Unknown-field retries reuse encoded input.
- Examples and documentation clarify defaults, unknown fields and provider usage.
- All 20 release modules use coordinated v1.2.0 tags. Lint covers every Go
  module, including the separate benchmark module; formatting checks also cover
  build-tagged examples.

### Fixed

- `Explain` masks secret fields loaded through YAML even when `json:"-"` omits
  them from diagnostic trees or `yaml:",inline"` flattens their input paths.
- Azure/GCP cloud modules pin the HTTP/2 security fixes in `golang.org/x/net`
  v0.60.0. Azure App Configuration, Blob and Service Bus now require Go 1.26.
  Security validation uses Go 1.26.10 to include the crypto/x509 trust fix.

- Azure Blob errors hide SAS URLs and document contents while preserving
  `errors.Is`/`errors.As`. The legacy `s3events.Watch` remains best-effort;
  reliable refresh-before-ack delivery uses the new SNS integration.
- S3 and GCS loading errors hide document and transport details while preserving
  error classification. AppConfig, GCS and Azure Blob honor cancellation while
  waiting for another load to finish.
- SQS acknowledgements use a separate five-second budget within the visibility
  margin. Pub/Sub documentation now describes asynchronous acknowledgement
  reporting accurately, and secret resource selectors reject malformed paths.
- OPA uses the current SDK import path with existing Rego v0 syntax compatibility.

- TOML dumps identify the path of null array elements without duplicate error prefixes.

- `fastconfd` logs reload/dump error details and returns only a generic HTTP error.

- Diagnostic diffs resolve literal dotted map keys by path segments, preserving correct values.

- Mismatched `WithValidate` / `WithPolicy` types fail construction with both type names.

- nil `Option` and `ReloadOption` values are ignored instead of panicking.

- Provider events dropped on a full queue now schedule one compensating refresh.

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
