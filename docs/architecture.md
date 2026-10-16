# FastConf Architecture

This page describes the FastConf v1.1.0 source layout and dependency direction.

## Directory Layout

```text
fastconf/                 package fastconf: public API and runtime coordination
├── contracts/            stable provider, generator and codec contracts
├── codec/                document decoders, registry and typed hooks
├── confmap/              merge, patch, path and scalar operations
├── transform/            raw-map transforms and schema migrations
├── feature/              feature rule evaluation
├── observe/              observer composition and adapters
├── policy/               policy contracts; opa/ is a separate module
├── providers/            provider implementations; s3/ is a separate module
├── internal/             private reusable implementation packages
├── integrations/         render, CLI and logging adapters
├── observability/        independent metrics and tracing modules
├── cue/ / validate/      optional validation modules
├── cmd/                  fastconfd, fastconfctl, fastconfgen
├── examples/             runnable usage examples
├── tests/                public API integration scenarios and resource soak
├── docs/                 architecture, runtime contract and user guides
└── tools/                repository checks and module inventory
```

Keep `Manager[T]` and `State[T]` in the root package. Their files share private
state and the single-writer lifecycle; moving them behind a second facade
would add forwarding without creating an independent component. Reusable
algorithms belong in the existing domain or `internal/` packages. Optional
external dependencies remain isolated by the module inventory.

## Placement Rules

| Feature | Implementation and tests | Examples and documentation |
|---|---|---|
| Manager lifecycle, reload and snapshots | Root package; public scenarios in `tests/integration/` | Root godoc examples, `examples/reload/`, runtime contract |
| Documents and transformations | `codec/`, `confmap/`, `transform/` | Typed-hooks, generators and merge recipes |
| Configuration sources | `providers/<source>/` | Source examples under `examples/`, including `examples/s3/` |
| Policy and validation | `policy/`, `cue/`, `validate/` | Policy and field-meta recipes |
| Framework adapters | `integrations/<domain>/<adapter>/` | pflag example in `examples/pflag/` |
| Observation and diagnostics | Root event/view API, `observe/`, `observability/` | Observer, observability and introspection recipes |
| CLI and sidecar | `cmd/<command>/`; shared setup in `cmd/internal/cli/` | Sidecar recipe and release guide |
| Resource stability | `tests/soak/` | Performance notes and `make soak` |

Place each unit test next to its implementation. Put a cross-package public
API scenario in `tests/integration/`; keep process-wide resource measurements
in `tests/soak/`. Keep runnable examples together under `examples/`, including
S3 and pflag. Those two optional programs use `go:build ignore` to keep root
dependencies isolated; `make example` runs their files explicitly with the local
adapter modules in a temporary workspace.

Choose a package boundary for an independently reusable responsibility, not
just to shorten a directory listing. Keep existing exported import paths stable.
Add a `go.mod` only when a dependency needs independent isolation. Do not
maintain a second module list: `tools/modules.sh` discovers the manifests.

[Documentation navigation](README.md) distinguishes current recipes and
contracts from historical review records. When moving code, update its commands,
relative links and this ownership map in the same change.

## Root File Responsibilities

| Files | Responsibility |
|---|---|
| `manager.go` | `New`/`Load` shared initialization, lifecycle, `Get` and `Snapshot`. |
| `config.go`, `options.go` | Resolved options and validation, public option builders, defaults and coalescing presets. |
| `reload.go` | Public `Reload` options, override copying, assembly/commit orchestration, watch-path refresh and reload observation. |
| `reload_queue.go` | Request queue, caller cancellation and the single writer loop used by reload, plan and rollback. |
| `assemble.go` | Discover and rank file, generator, provider and override layers; connect scanning to the codec registry. |
| `layer_cache.go`, `input_fingerprint.go` | Decoded-layer/metadata caches and the conservative unchanged-input shortcut. |
| `pipeline.go`, `stages.go` | Ordered stage definition, stage tracing and execution. |
| `decode.go`, `policy.go` | JSON/YAML decoding, typed-hook setup and warnings; policy evaluation. |
| `commit.go` | Hash comparison, publication, history retention and commit notifications. |
| `state.go`, `diff.go`, `dump.go` | Immutable snapshots, typed hashes, cached diagnostic trees, redacted diffs and YAML/JSON/TOML output. |
| `provenance.go`, `secret.go` | Source/origin/cause metadata and public secret interfaces backed by private implementations. |
| `plan.go`, `history.go`, `ring.go` | Dry-run previews and bounded history with rollback. |
| `subscribe.go` | Extracted-value comparisons, subscriptions and callback panic isolation. |
| `file_watch.go`, `provider_watch.go` | File-watch coordination and pause controls; provider event streams, revisions and reconnects. |
| `observe.go`, `tracing.go`, `errors.go` | Observer events, tracer interfaces and error delivery. |
| `doc.go` | Package-level documentation and recommended API entry points. |

`New` and `Load` call `newManager` to resolve options and prepare typed hooks.
Both load synchronously; only `New` starts background work. Later operations
enter `reload_queue.go`. Reload and Plan share the assembly and stage code;
Plan collects findings without publishing. Commit and rollback both use
`publishSnapshot` so history, observers and subscribers follow one publication
path. Reload refreshes file-watch paths after assembly, even when the candidate
fails or its value is unchanged.

The writer owns layer caches, merge-key metadata and the input fingerprint.
Readers load an immutable `State` atomically. Subscription registration uses a
separate mutex; callbacks run after its release, on the writer. File watching
and provider watching produce reload requests rather than modifying snapshots.

## Tests and Naming

Unit tests live beside the package whose functions they exercise. The root
keeps tests that need access to runtime internals, plus benchmarks and executable
godoc examples. Public API scenarios are grouped
under `tests/integration/` in `package integration_test`.

| Location | Responsibility |
|---|---|
| Root `manager_test.go`, `options_test.go`, `watch_test.go` | Construction, options, lifecycle, and watch internals |
| Root `assemble_test.go`, `pipeline_test.go`, `reload_test.go`, `cache_test.go` | Layer selection, stage execution, queue handling, and caches |
| Root `state_test.go`, `diagnostics_test.go`, `history_test.go`, `secret_test.go`, `plan_test.go`, `errors_test.go` | State publication, views, history, secrets, preview, and error contracts |
| `tests/integration/construction_test.go`, `options_test.go`, `decode_test.go`, `ini_test.go`, `validation_test.go` | Typed public entry points, axes, decoding, INI source integration, defaults, and validation |
| `tests/integration/reload_test.go`, `file_watch_test.go`, `provider_watch_test.go`, `provider_test.go` | Reload failure safety, shutdown, event sources, and provider ownership |
| `tests/integration/state_test.go`, `provenance_test.go`, `errors_test.go` | Public diagnostic views, provenance, and the error stream |
| `tests/integration/observability_test.go`, `subscribe_test.go`, `transform_test.go` | Observers, tracing, logs, subscriptions, and transforms |
| `codec/`, `confmap/`, `internal/*/` | Component function tests and fuzz targets |
| `providers/*/`, `integrations/*/`, policy backends, and `cmd/*/` | Package-specific behavior, including separate Go modules |
| `examples/{basic,ini,tenant,reload,features}/` | Runnable scenario tests with local configuration fixtures |
| `tests/soak/` | Opt-in mixed workload and process-wide resource recovery |
| `examples/s3/`, `examples/pflag/` | Tracked optional programs explicitly run by `make example` |

Root unit fixtures live in `test_helpers_test.go`; shared integration fixtures
live in `tests/integration/helpers_test.go`. Helpers used by only one suite stay
with that suite. `internal/testutil` supplies cross-package filesystem, polling,
provider, and tracing fixtures. The log adapters share contract cases in
`integrations/log/internal/logtest` and retain backend-specific cases locally.

Benchmarks live in `bench_test.go` and `bench_public_test.go`; executable godoc
examples live in `example_api_test.go`. The resource soak lives in
`tests/soak/soak_test.go`. Fuzz targets stay with their component in `fuzz_test.go` or a
named fuzz file.

Test names describe the current API and observable behavior. Use table cases
for related inputs, verify errors and retained state on failure, and use
channels or bounded condition polling for asynchronous progress. Timing tests
retain waits when the duration itself is the behavior under test. Regression
cases join the suite responsible for that behavior.

`make test` runs root-module race tests, including `tests/integration/`;
`make test-workspace` covers every local module in a temporary workspace;
`make check` checks formatting, dependency boundaries and tooling regressions. `make example`
runs godoc examples, scenario tests and the S3/pflag programs in a temporary
workspace; CI runs the same target. To measure coverage across unit and
integration packages together, use `go test -coverpkg=./... -coverprofile=coverage.out ./...`.

Use domain names for files and verb/object names for actions. For example,
`notifySubscribers` dispatches subscriptions, while `startWatcher` starts file
watching. `pipelineState` holds stage data; `context.Context` controls operation
lifetime. Test names describe the API and expected behavior, such as
`TestReload_WithOverride_DeepCopyAtCall`; fixture types describe their data,
such as `overrideConfig`, rather than using issue numbers. Exported API names
and import paths remain compatibility boundaries.

## Dependency Direction

```
fastconf  →  internal/{coalesce,fcerr,obs,provenance,scan,secret,typeinfo,watcher}
          →  codec, confmap, contracts, policy

transform, providers/*, feature and observe sit beside the root: users import
them and hand the results to root options (WithTransform, WithProvider, …).

Public domain packages form a small DAG:
  codec       → contracts
  transform   → confmap
  providers/* → codec, confmap, contracts

observe imports fastconf (it builds on the Observer type); fastconf does not
import observe. internal/* packages are implementation details and cannot be
imported by external consumers.
```

Within `contracts`, `provider.go` owns provider metadata, priorities and reader
limits, `generator.go` owns generators and raw layers, and `decoder.go` owns the
document decoding interface. Public aliases in the root package live with their
domain: provenance, secrets and tracing.

Builds enforce Go's import-cycle and internal-package rules. `tools/modules.sh` discovers
modules directly from their `go.mod` files (including `cmd/fastconfgen`);
`make test-workspace` creates a temporary workspace from that discovery,
while `make test-all` checks independent dependency resolution.
