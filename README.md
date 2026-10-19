# FastConf

[English](README.md) · [中文](README.zh.md)

FastConf loads layered configuration into typed Go values. It supports YAML,
JSON, TOML and INI, environment variables, flags and remote providers. A manager
serializes reloads and publishes snapshots atomically; `Get()` is a lock-free,
zero-allocation read. Failed loads or validation keep the previous snapshot.

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

Documentation follows this checkout. Features added after v1.1.0 are marked;
use the documentation at your installed tag for released behavior. For v0.20.0
upgrades, see the [migration guide](docs/cookbook/migration-v1.md).

## Installation

The root module requires Go 1.24+. Optional modules declare their own minimum
Go version; install only the adapters you use.

```sh
go get github.com/fastabc/fastconf@v1.1.0
```

## Quick start

Choose the entry point that matches the configuration lifecycle:

| Need | API | Guide |
|---|---|---|
| Read exactly one required file at startup | `LoadFile[T](path)` → `*T` | [Single file](docs/getting-started/load.md); added after v1.1.0 |
| Load layers once, with options and diagnostics | `Load[T](ctx, opts...)` → `*State[T]` | [Layered example](examples/basic/README.md) |
| Reload configuration while running | `New[T](ctx, opts...)` → `*Manager[T]` | [Dynamic guide](docs/getting-started/dynamic.md) |

Create `conf.d/base/00-app.yaml`:

```yaml
server:
  port: 8080
```

Save this complete program as `main.go` in your application module:

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/env"
)

type Config struct {
    Server struct {
        Port int `json:"port" fc:"min=1,max=65535"`
    } `json:"server"`
}

func main() {
    state, err := fastconf.Load[Config](context.Background(),
        fastconf.WithDir("conf.d"),
        fastconf.WithProvider(env.New("MYAPP_")),
        fastconf.WithUnknownFields(fastconf.UnknownError),
    )
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("port=%d", state.Value().Server.Port)
}
```

```sh
go run .                          # port=8080
MYAPP_SERVER_PORT=9090 go run .     # port=9090
MYAPP_SERVER_PORT=0 go run .        # validation error
```

Input formats use `json` struct tags by default. `Load` starts no background
workers; its value and all reachable maps, slices and pointers must stay
read-only. For runtime updates, use `New`, enable file watching with `WithWatch`,
and close the manager on shutdown. See the complete [dynamic example](examples/dynamic/README.md).

Run bundled examples from the repository root:

```sh
go run ./examples/basic
go run ./examples/reload
```

### Exactly one required file

`LoadFile` was added after v1.1.0. With the `Config` type and YAML above:

```go
cfg, err := fastconf.LoadFile[Config]("conf.d/base/00-app.yaml")
if err != nil {
    return err // Inside a function returning error.
}
log.Printf("port=%d", cfg.Server.Port)
```

Only that file is read; env and profile discovery do not participate. Unknown
keys and missing files fail. The caller owns the returned value, with no
manager to close. See the [single-file guide](docs/getting-started/load.md).

## Features

| Area | Capabilities and recipes |
|---|---|
| Sources | [Files and profiles](docs/cookbook/sources/layers.md), [INI](docs/cookbook/sources/ini.md), [env and dotenv](docs/cookbook/sources/env-replacer.md), [labels](docs/cookbook/sources/labels.md), [HTTP](docs/cookbook/sources/http.md), [Vault](docs/cookbook/sources/vault.md), [Consul](docs/cookbook/sources/consul.md) |
| Merge and conversion | Base/overlay layers, RFC 6902 patches, [keyed lists](docs/cookbook/processing/strategic-merge.md), [generators](docs/cookbook/processing/generators.md), [typed hooks](docs/cookbook/processing/typed-hooks.md) |
| Validation and secrets | [`fc` tags and defaults](docs/cookbook/processing/field-meta.md), [Go/OPA/CUE policies](docs/cookbook/processing/policy.md), [resolution and redaction](docs/cookbook/processing/secrets.md) |
| Runtime updates | [Subscriptions](docs/cookbook/runtime/subscribe.md), [failure handling and overrides](docs/cookbook/runtime/reload-policy.md), [Plan](docs/cookbook/runtime/plan.md), [history and rollback](docs/cookbook/runtime/introspect.md) |
| Operations | [Observers](docs/cookbook/observability/observer.md), [metrics and tracing](docs/cookbook/observability/observability.md), [logging](docs/cookbook/observability/log.md), [timeouts](docs/cookbook/runtime/provider-timeouts.md) |
| Deployment | [Kubernetes](docs/cookbook/deployment/k8s.md), [sidecar](docs/cookbook/deployment/sidecar.md), [tenants](docs/cookbook/deployment/tenant.md), [transport reference implementations](docs/cookbook/sources/cross-process.md) |
| Application features | [Feature targeting and percentage rollouts](docs/cookbook/features/features.md) |

Layer precedence is files → generators → providers → one-shot overrides.
Provider priorities only order providers. FastConf borrows base/overlay and
patch concepts from Kustomize; it does not read `kustomization.yaml` or implement
the full Kubernetes strategic-merge protocol.

The [runtime contract](docs/design/spec.md) defines lifecycle, precedence,
decoding and ownership. The [extensions guide](docs/getting-started/extensions.md)
lists optional modules, including S3, pflag, validation, logging and telemetry.
NATS and Redis Streams are application-adaptable examples, not stable providers.

## Loading and reload pipeline

`Load` returns a snapshot without workers. `New` loads synchronously, starts
the writer and configured watchers, and provides `Get`, `Snapshot`, `Reload`,
`Plan`, `Errors` and history. The constructor context bounds initialization;
close the manager to stop background work.

The following blocks are integration fragments using application types and
values.

```go
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithUnknownFields(fastconf.UnknownError),
    fastconf.WithWatch(fastconf.Watch{}), // Enable file watching.
    fastconf.WithHistory(4),             // Retain up to four prior snapshots.
)
if err != nil {
    return err // Inside a function returning error.
}
defer mgr.Close()
cfg := mgr.Get() // Read once per business operation; keep the entire tree read-only.
_ = cfg
// Keep the manager alive until the service owner shuts down.
```

```text
assemble -> merge -> transform -> secret -> typed-hooks -> decode
         -> field-meta -> validate -> policy -> hash -> publish
```

A rejected candidate preserves the live snapshot and generation. Equal typed
hashes skip publication. `Errors()` is bounded and evicts the oldest pending
error on overflow; consume it for ongoing failures. `WithUnknownFields` controls
unknown struct keys; the default `UnknownWarn` needs `WithLogger` to be visible.
`WithStrictMerge` controls raw merge type conflicts and file discovery instead.

### Files, profiles and merge rules

`base/*` loads first, followed by matched `overlays/*`, then `WithAxes` directories
such as region or host. Files and matching profile directories use lexical
order; the order of names in `Profile.Names` does not set overlay precedence.

```go
fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}})
fastconf.WithAxes(fastconf.Axis{Dir: "regions", Env: "DEPLOY_REGION"})
fastconf.WithMergeKeys(map[string]string{"listeners": "name"})
```

Maps merge recursively; scalars and lists replace by default. `mergeKeys`
aligns list objects by a non-empty string key. `.patch.json` / `.patch.yaml`
files apply RFC 6902 operations in layer order. Root `_meta.yaml` uses `spec`
for defaults and merge options; overlay `_meta.yaml` uses top-level `match`
expressions. See [files, profiles and patches](docs/cookbook/sources/layers.md).

### Providers and generators

| Source | Entry point | Behavior |
|---|---|---|
| File / bytes | `source.NewFile(path)`, `source.NewBytes(name, codec, data)` | Auxiliary decoded documents; missing `NewFile` files contribute an empty layer |
| Env / dotenv | `env.New("MYAPP_")`, `dotenv.New("MYAPP_", ".env")` | Underscores become path separators; dotenv is fallback, process env wins |
| Flags | `cliflag.New(values)` | Pass explicitly changed flags; pflag has an optional adapter |
| Labels / Downward API | `labels.New(input, opts)`, `k8s.NewDefault()` | Dotted config labels or raw namespaced pod metadata |
| HTTP / Vault / Consul | `providers/http`, `providers/vault`, `providers/consul` | Remote documents or KV, with native polling/blocking-query watchers |
| S3 | Separate `providers/s3` module | Object loading and optional change-event integration |

Providers implement `contracts.Provider` (`Name`, `Load`). Optional `Describe`
supplies priority/file paths; optional `contracts.Watcher` supplies native events
with `Watch(ctx, from)`. Provider watchers start with `New` independently of
`WithWatch`. Configure providers before registration; fluent methods mutate them.
Within the provider class, higher priority wins and registration order breaks ties.

`WithGenerator` accepts `contracts.Generator` implementations producing encoded
`RawLayer` values. Generators run during assembly before providers and have no
built-in watch channel. Transforms registered with `WithTransform` then rewrite
the merged tree, including schema migrations through `transform.NewChain`.
See [generators](docs/cookbook/processing/generators.md) and [extensions](docs/getting-started/extensions.md).

### Types, validation and secrets

Document format is independent of struct decoding: the default JSON bridge uses
`json` tags; `WithDecodeFormat(fastconf.YAML)` selects YAML tags. Default typed
hooks convert duration strings and primitive string values from env/INI.

The type fragment below imports `time`.

```go
type Database struct {
    DSN     string        `json:"dsn" fc:"required,secret"`
    Pool    int           `json:"pool" fc:"min=1,max=100"`
    Timeout time.Duration `json:"timeout"`
}
```

`fc` defaults fill Go zero values after decoding, followed by `Defaulter.Defaults`.
Use base-layer defaults when explicit false, 0 or empty strings must survive.
Field constraints run before `WithValidate` and `WithPolicy`; policy warnings
allow publication and error-severity findings reject it. Tags do not validate
collection elements; use a validator for those checks.

`SecretResolver` resolves references before typed decoding; it does not decrypt
SOPS/age/KMS formats without an application adapter. `fc:"secret"` and
`WithSecretPaths` mask diagnostic output independently of resolution. `Get()`
and `Value()` still expose plaintext to application code. See
[field metadata](docs/cookbook/processing/field-meta.md), [policies](docs/cookbook/processing/policy.md)
and [secrets](docs/cookbook/processing/secrets.md).

### Subscriptions, preview and recovery

```go
cancel := fastconf.Subscribe(mgr,
    func(c *Config) *int { return &c.Server.Port },
    func(old, next *int) { log.Printf("port: %d -> %d", *old, *next) },
)
defer cancel()

if err := mgr.Reload(ctx, fastconf.WithReason("admin"),
    fastconf.WithOverride(map[string]any{"server": map[string]any{"port": 9090}}),
); err != nil {
    return err
}
```

Subscriptions compare selected values after publication; `WithEqual` customizes
equality. Callbacks run on the writer, must return quickly and must not wait on
that manager's reload/Plan/rollback/shutdown. Signal a bounded worker for costly
resource updates. An override applies to one reload and does not edit sources.

`Plan(ctx)` returns `Proposed`, `Diff`, `Validators` and `Policies` without
publishing. Inspect validator errors and policy severity even when Plan returns
nil error. Reload reads inputs again. `Pause` suppresses watcher triggers;
`Resume` does not replay ignored events, so reload explicitly after a paused batch.

`Snapshot().Map/Dump/Diff/Explain` provide masked diagnostics; enable
`WithProvenance` for origins. `Unredacted().Map/Dump` exposes plaintext explicitly.
`History().List()` returns prior snapshots oldest-first; rollback republishes a
retained snapshot without validation or source repair. History is nil when disabled.
See [subscriptions](docs/cookbook/runtime/subscribe.md), [Plan](docs/cookbook/runtime/plan.md)
and [diagnostics/history](docs/cookbook/runtime/introspect.md).

### Observability and application features

`WithObserver` receives reload, stage, provider-error, drop and commit events.
`observe.Multi`, `JSONLines`, `Metrics` and `Func` compose audit and telemetry.
`observe.Async` adds bounded asynchronous delivery; its owner closes it after
the manager and supplies deadlines for external I/O. Log through `WithLogger`
and a standard `slog.Handler`, or an optional backend adapter.

Use one manager per tenant, selecting sources and credentials explicitly;
`WithTenant` only labels causes and policy input. Feature rules are ordinary
configuration fields evaluated with `feature.Eval`: targets precede deterministic
percentage rollouts, then the rule default. The evaluator is not an OpenFeature
SDK provider. See [observability](docs/cookbook/observability/observability.md),
[tenants](docs/cookbook/deployment/tenant.md) and [feature flags](docs/cookbook/features/features.md).

## CLI tools

| Command | Purpose |
|---|---|
| `fastconfctl` | Offline `dump`, `diff`, `validate` and `explain` |
| `fastconfd` | HTTP configuration snapshots, manual reload and SSE notifications; [sidecar recipe](docs/cookbook/deployment/sidecar.md) |
| `fastconfgen` | Generate Go structs from YAML; separate module |

Install from source at a matching version, or use binaries from
[GitHub Releases](https://github.com/fastabc/fastconf/releases):

```sh
go install github.com/fastabc/fastconf/cmd/fastconfctl@v1.1.0
go install github.com/fastabc/fastconf/cmd/fastconfd@v1.1.0
go install github.com/fastabc/fastconf/cmd/fastconfgen@v1.1.0
fastconfctl dump -dir conf.d -profile prod -format yaml
fastconfctl diff -dir conf.d -from dev -to prod -json
fastconfctl validate -dir conf.d -profile prod
fastconfctl explain -dir conf.d -profile prod server.port
fastconfgen -in conf.d/base/00-app.yaml -pkg config -type Config
```

`fastconfctl validate` loads a generic map; it does not validate your application's
struct tags or business policies. In this checkout (after v1.1.0), CLI diagnostic
output masks common credential keys by default; library structs need `fc:"secret"` or `WithSecretPaths`.
See [diagnostics](docs/cookbook/runtime/introspect.md).

## Development and documentation

```sh
make build       # root module and fastconfgen
make test        # root race tests
make example     # executable examples, including optional integrations
make check       # repository checks; see tools/README.md
```

- [Documentation index](docs/README.md): guides and recipes by function.
- [Examples](examples/README.md): complete programs, fixtures and expected output.
- [Repository tooling](tools/README.md): module checks and development commands.
- [Architecture](docs/architecture.md): package responsibilities and dependency direction.
- [Performance notes](docs/design/perf.md): reproducible measurements and read-path budget.
- [Release guide](RELEASING.md): coordinated module tags and distribution.
- [API reference](https://pkg.go.dev/github.com/fastabc/fastconf): published Go API.

## License

[MIT](LICENSE) · Copyright (c) 2026 FastAbc
