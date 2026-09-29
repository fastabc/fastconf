# FastConf Architecture

This page is the canonical directory and dependency overview for the current
layout. Update the root-file and top-level-directory tables when moving files.

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
├── docs/                 architecture, runtime contract and user guides
└── tools/                repository checks and module inventory
```

Keep `Manager[T]` and `State[T]` in the root package. Their files share private
state and the single-writer lifecycle; moving them behind a second facade
would add forwarding without creating an independent component. Reusable
algorithms belong in the existing domain or `internal/` packages. Optional
external dependencies remain isolated by the module inventory.

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
| `provenance.go`, `aliases.go` | Source/origin/cause metadata and public secret interfaces backed by private implementations. |
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

Tests live beside the package they exercise. Root tests follow runtime topics:
`manager`, `options`, `reload`, `state`, `diff`, `dump`, `provenance`, `history`,
`errors`, `subscribe`, `file_watch`, and `provider`. Use `_internal_test.go`
or `_public_test.go` when distinguishing a companion test package is useful.
Keep implementation-sensitive cases in `package fastconf`; external API cases
use `package fastconf_test`. Retain component integration tests with their
component, such as `codec/integration_test.go`.

`bench_test.go` and `bench_public_test.go` contain benchmarks; godoc examples
live together in `example_api_test.go`, including profile and provider scenarios.
The resource soak lives in `soak_test.go`.
Regression cases join the matching topic file. Split a large topic only when
there is a distinct responsibility, as with state views, dump formats and
history, rather than creating a file for each bug.

Use domain names for files and verb/object names for actions. For example,
`notifySubscribers` dispatches subscriptions, while `startWatcher` starts file
watching. `pipelineState` holds stage data; `context.Context` controls operation
lifetime. Test names describe the API and expected behavior, such as
`TestReload_WithOverride_DeepCopyAtCall`; fixture types describe their data,
such as `overrideConfig`, rather than using issue numbers. Exported API names
and import paths remain compatibility boundaries.

## Top-Level Directories

| Directory | Role |
|---|---|
| `cmd/` | Command binaries. |
| `contracts/` | Stable public interfaces. |
| `codec/` | Codec registry, built-in decoders, content-type lookup, typed hooks. |
| `confmap/` | `map[string]any` merge, path, label expansion, and scalar coercion helpers. |
| `cue/` | CUE sub-module. |
| `docs/` | Runtime contracts, cookbook, and design guides. |
| `examples/` | Runnable scenario examples outside the root package. |
| `feature/` | Feature flag rule evaluation. |
| `integrations/` | Optional integration adapters. |
| `internal/` | Private implementation packages protected by Go's internal boundary. |
| `observability/` | Independent metrics and tracing modules. |
| `observe/` | Observer building blocks: Multi, Async, JSONLines, Metrics. |
| `policy/` | Policy backends. |
| `providers/` | Built-in and satellite providers. |
| `tools/` | Repository guard scripts. |
| `transform/` | Raw-map transform functions and schema migration helpers. |
| `validate/` | Validation playground sub-module. |
| `.github/` | CI workflows. |

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

Builds enforce Go's import-cycle and internal-package rules. `tools/modules.sh` discovers
modules directly from their `go.mod` files (including `cmd/fastconfgen`);
`make test-workspace` creates a temporary workspace from that discovery,
while `make test-all` checks independent dependency resolution.
