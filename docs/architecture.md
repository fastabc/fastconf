# Architecture

This page maps responsibilities in the current checkout. The
[runtime contract](design/spec.md) defines observable behavior; exported Go
comments define API signatures. Prefer those sources over a copied function
inventory when changing implementation files.

## Package boundaries

| Location | Responsibility |
|---|---|
| Root `fastconf` package | `LoadFile`, `Load`, `New`, Manager lifecycle, serialized reload/Plan/rollback and immutable snapshots |
| [`contracts/`](../contracts) | Provider, watcher, decoder and generator interfaces |
| [`codec/`](../codec) | Document decoders, registry and typed hooks |
| [`confmap/`](../confmap) | Merge, patch, paths, cloning and coercion |
| [`transform/`](../transform) | Raw-tree transformations and schema migration chains |
| [`providers/`](../providers) | File, env, flag, label and remote sources; S3 is a separate module |
| [`policy/`](../policy), [`cue/`](../cue), [`validate/`](../validate) | Policies and optional validation backends |
| [`observe/`](../observe), [`observability/`](../observability) | Event composition and optional telemetry backends |
| [`feature/`](../feature) | Configuration-backed feature rule evaluation |
| [`integrations/`](../integrations) | Rendering, CLI and logging adapters |
| [`internal/`](../internal) | Private scanning, watching, coalescing, metadata, provenance, secret and shared utilities |
| [`cmd/`](../cmd) | `fastconfctl`, `fastconfd` and independently versioned `fastconfgen` |
| [`examples/`](../examples) | Complete programs, fixtures and transport reference implementations |
| [`tests/`](../tests) | Public API integration scenarios and resource soak |
| [`tools/`](../tools) | Repository checks and discovered module inventory |

Keep `Manager[T]` and `State[T]` in the root package: they share private state
and the writer lifecycle. Reusable algorithms belong in domain packages or
`internal/`; choose boundaries by responsibility, not file size. Preserve
exported import paths. Add a module only when dependency isolation requires it.
The [extensions guide](getting-started/extensions.md) lists optional adapters;
`tools/modules.sh` discovers their manifests for checks and release tooling.

## Runtime responsibilities

| Concern | Source entry points |
|---|---|
| Public entry points and options | [`load_file.go`](../load_file.go), [`manager.go`](../manager.go), [`options.go`](../options.go), [`config.go`](../config.go) |
| File discovery and source assembly | [`assemble.go`](../assemble.go), [`internal/scan/`](../internal/scan) |
| Caching and unchanged-input detection | [`layer_cache.go`](../layer_cache.go), [`input_fingerprint.go`](../input_fingerprint.go) |
| Writer, reload and pipeline | [`reload_queue.go`](../reload_queue.go), [`reload.go`](../reload.go), [`pipeline.go`](../pipeline.go), [`stages.go`](../stages.go) |
| Publication, snapshots and diagnostics | [`commit.go`](../commit.go), [`state.go`](../state.go), [`dump.go`](../dump.go), [`diff.go`](../diff.go), [`provenance.go`](../provenance.go) |
| Preview, recovery and callbacks | [`plan.go`](../plan.go), [`history.go`](../history.go), [`subscribe.go`](../subscribe.go) |
| File/provider triggers and lifecycle events | [`file_watch.go`](../file_watch.go), [`provider_watch.go`](../provider_watch.go), [`observe.go`](../observe.go) |

Initialization loads synchronously. Only `New` starts the writer and configured
watchers; later reloads, plans and rollbacks use that writer. Reload and Plan
share assembly and pipeline stages. Publication updates the snapshot, retains
enabled history, emits commit events and invokes subscribers. File/provider
watchers enqueue work instead of modifying snapshots directly.

The writer owns layer caches, merge metadata and the input fingerprint.
Readers load the snapshot atomically. Subscription registration has a separate
mutex; callbacks run after releasing it. Callback and cancellation limits are
specified in the [runtime contract](design/spec.md#reads-and-writes).

## Dependency direction

```text
fastconf -> contracts, codec, confmap, policy, internal/*
codec -> contracts
transform -> confmap
providers/* -> contracts, codec, confmap, internal/* (as needed)
observe -> fastconf
```

The root does not import `observe`: applications compose observers and pass
them to options. Domain packages can be used without a manager where their
interfaces permit. Go enforces import cycles and `internal` access boundaries;
optional external dependencies stay in their adapter modules.

## Tests and checks

Place unit tests next to their implementation. Cross-package public scenarios
belong in `tests/integration/`; process-wide resource measurements belong in
`tests/soak/`. Shared internal fixtures live in `internal/testutil/`.
Executable Go API examples live in [`example_api_test.go`](../example_api_test.go).
Runnable programs keep fixtures and scenario tests in their example directories.
S3 and pflag programs use `go:build ignore` and are run explicitly with their
adapter modules; see the [example index](../examples/README.md).

| Check | Scope |
|---|---|
| `make test` | Root-module race tests, including public integration scenarios |
| `make test-workspace` | Every local module in a temporary workspace |
| `make test-all` | Modules against their declared dependencies |
| `make example` | Executable API examples, scenario tests and optional programs |
| `make check` | Formatting, dependency metadata and tooling regressions |
| `make soak` | Resource recovery under a mixed workload |

Full commands belong in [repository tooling](../tools/README.md), and benchmark
contracts in [performance notes](design/perf.md). When moving a file, update
this map and its links together; avoid reproducing individual test-name lists.
