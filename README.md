# FastConf

[English](README.md) · [中文](README.zh.md)

FastConf brings files, environment variables and cloud configuration into one typed
Go configuration. Load once for a CLI or keep a service current with validated hot
reloads, atomic snapshots and a lock-free, zero-allocation `Get()`.
Failed reloads keep the application on its last valid configuration.

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

This README presents the current v1.6.0 feature set; additions marked v1.6.0
refer to this checkout. Version-pinned installation examples below use v1.5.0.
Read the documentation at your installed tag for release-specific behavior.
For v0.20.0 upgrades, see the
[migration guide](docs/cookbook/migration-v1.md); for v1.4.0, see the
[v1.5.0 upgrade notes](docs/cookbook/migration-v1.md#upgrading-from-v140-to-v150).

## Why FastConf

- **One struct, many sources.** Combine YAML, JSON, TOML, INI, env, flags and remote
  providers; use profiles, region/host layers, patches and keyed-list merging for deployment differences.
- **Configuration that is easy to write.** In v1.6.0, readable `default` tags preserve
  explicit zero values, KV strings become typed lists, and interpolation connects env values and config fields.
- **Controlled runtime changes.** Preview with `Plan`, validate before publication,
  subscribe to a selected subtree, and restore a retained snapshot with history rollback.
- **Cloud refresh with less application code.** In v1.6.0, Manager supervises notification
  consumers and periodically reconciles sources through `WithNotifier` and `WithResync`.
- **Secrets masked in diagnostics.** In v1.6.0, chain Vault, age and application resolvers;
  resolved values are automatically redacted in `Map`, `Dump`, `Diff` and `Explain`.
- **Documentation from the config type.** In v1.6.0, generate JSON Schema, annotated
  YAML and env examples from the same struct, defaults and field descriptions.
- **Visibility and deployment options.** Trace value origins, audit changes, export metrics
  and traces, or expose snapshots through the CLI/sidecar. Cloud SDKs and telemetry backends are optional modules.

## Installation

The root module requires Go 1.25+. Optional modules declare their own minimum
Go version; install only the adapters you use.

```sh
go get github.com/fastabc/fastconf@v1.5.0
```

## Quick start

Choose the entry point that matches the configuration lifecycle:

| Need | API | Guide |
|---|---|---|
| Read exactly one required file at startup | `LoadFile[T](path)` → `*T` | [Single file](docs/getting-started/load.md); added in v1.2.0 |
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

`LoadFile` was added in v1.2.0. With the `Config` type and YAML above:

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

| Capability | What you can build | Guide |
|---|---|---|
| Layered, typed loading | Files, env/dotenv, flags, labels, HTTP, Vault, Consul and AWS/Azure/GCP sources behind one config type | [Sources](docs/cookbook/README.md#sources-and-layering) |
| Deployment-specific overrides | Base/overlay profiles, region/host axes, RFC 6902 patches, keyed lists and generated layers | [Layers](docs/cookbook/sources/layers.md), [keyed lists](docs/cookbook/processing/strategic-merge.md), [generators](docs/cookbook/processing/generators.md) |
| Readable defaults and conversion (v1.6.0) | `default:"5s"`, `default:"a,b"`, named primitives, comma-separated/JSON/indexed-map scalar lists; Azure JSON values are opt-in | [Field metadata](docs/cookbook/processing/field-meta.md), [typed hooks](docs/cookbook/processing/typed-hooks.md) |
| Env and field interpolation (v1.6.0) | `${VAR:default}`, required values and `${server.base_url}` references with cycle detection | [Interpolation](docs/cookbook/processing/interpolation.md) |
| Validation before publication | Field constraints, Go validators and policies, with optional OPA/CUE adapters | [Validation](docs/cookbook/processing/field-meta.md), [policies](docs/cookbook/processing/policy.md) |
| Live updates and recovery | File/provider watchers, subtree subscriptions, one-shot overrides, Plan previews, history and rollback | [Subscriptions](docs/cookbook/runtime/subscribe.md), [Plan](docs/cookbook/runtime/plan.md), [history](docs/cookbook/runtime/introspect.md) |
| Managed cloud notifications (v1.6.0) | SNS/SQS, Service Bus or Pub/Sub consumers with Manager-owned restart, shutdown and periodic reconciliation | [Cloud notifications](docs/cookbook/sources/cloud-notifications.md) |
| Secret resolution and masking (v1.6.0) | Ordered resolvers, Vault KV v2 references, optional age decryption and automatic diagnostic redaction | [Secrets](docs/cookbook/processing/secrets.md) |
| Schema and examples (v1.6.0) | Draft 2020-12 JSON Schema, annotated YAML and env examples from struct tags | [Generation guide](docs/cookbook/processing/schema.md), [runnable example](examples/schema/README.md) |
| Operations and deployment | Explain origins, masked diffs, observers, metrics, traces, logging, CLI, Kubernetes sidecar and one Manager per tenant | [Observability](docs/cookbook/observability/observability.md), [sidecar](docs/cookbook/deployment/sidecar.md), [tenants](docs/cookbook/deployment/tenant.md) |
| Feature rules | Configuration-backed targeting and deterministic percentage rollouts | [Feature flags](docs/cookbook/features/features.md) |

Layer precedence is files → generators → providers → one-shot overrides.
Provider priorities only order providers. FastConf borrows base/overlay and
patch concepts from Kustomize; it does not read `kustomization.yaml` or implement
the full Kubernetes strategic-merge protocol.

The [runtime contract](docs/design/spec.md) defines lifecycle, precedence,
decoding and ownership. The [extensions guide](docs/getting-started/extensions.md)
lists optional modules, including S3, pflag, validation, logging and telemetry.
NATS and Redis Streams are application-adaptable examples, not stable providers.

## Benchmarks

Selected medians for **FastConf v1.6.0** from the
[2026-11-08 comparison](benchmarks/compare/REPORT.zh.md):
Apple M5 Pro, Darwin/arm64, Go 1.26.10, GOMAXPROCS=4, GOGC=100;
six 500 ms runs per case. FastConf uses local source commit `d0364e0`;
the report records source hashes, fixed dependency versions and scope.

| Workload | FastConf v1.6.0 | Viper 1.21.0 | Koanf 2.3.0 | Cleanenv 1.5.0 |
|---|---:|---:|---:|---:|
| 4 KiB YAML startup (ms/op) | 0.1186 | 0.1012 | 0.1291 | 0.0691 |
| 64 KiB valid reload (ms/op) | 1.6452 | 1.5454 | 1.9950 | — |
| Published struct hot read (ns/op) | 0.3900 | 0.4181 | 0.4363 | — |

All three measured hot-read paths allocate 0 B/op and 0 allocs/op. Viper and
Koanf use comparison adapters that decode, validate, deduplicate and atomically
publish typed snapshots; hot reads do not perform string-key lookup. Sub-nanosecond
figures measure tight-loop throughput, not request latency. Results depend on the
workload and machine; cloud-service latency is outside this comparison.

See the [comparison methodology and runner](benchmarks/compare/README.md) to
reproduce the measurements on an idle machine, and the
[performance contract](docs/design/perf.md) for the Get allocation guard,
reload benchmarks and profiling commands.

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
assemble -> merge -> transform -> secret -> defaults -> typed-hooks -> decode
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
Non-empty axis values (including hostname fallbacks) must be single directory
names; `.` / `..` and path separators are rejected with an axis warning.

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

| Provider / entry point | Watch changes | Notifier | Features |
|---|---|---|---|
| `source.NewFile` | Shared file watcher | — | Decoded document; missing file contributes an empty layer |
| `source.NewBytes` | — | — | Copied in-memory document with an explicit codec |
| `source.NewFS` | — | — | Required document from embedded or virtual `fs.FS`; added in v1.2.0 |
| `env.New` | — | — | Process environment; underscores become path separators |
| `dotenv.New` | Shared file watcher | — | Dotenv fallback; process environment wins |
| `cliflag.New` | — | — | Explicitly changed flags; optional pflag adapter |
| `labels.New` | — | — | Dotted configuration labels with configurable mapping |
| `k8s.NewDefault` | Shared file watcher | — | Raw namespaced Downward API metadata |
| `providers/http` | Native polling | — | Remote document, ETag/content hash |
| `providers/vault` | Native metadata polling | — | KV v2, optional authentication maintenance; field resolver in v1.6.0 |
| `providers/consul` | Native blocking query | — | KV tree or document |
| [`appconfig`](providers/appconfig) | Via notifier + periodic reload | SNS → SQS | Deployed document, session tokens, service-directed poll intervals |
| [`s3`](providers/s3) | Via notifier + periodic reload | SNS → SQS | ETag conditional reads, optional fixed VersionID |
| [`parameterstore`](providers/parameterstore) | Via notifier + periodic reload | EventBridge → SNS → SQS | Recursive paginated tree, string values, optional SecureString decryption |
| [`azappconfig`](providers/azappconfig) | Via notifier + periodic reload | Event Grid → Service Bus | One prefix/label, colon-separated keys; `WithJSONValues()` decodes JSON settings in v1.6.0 |
| [`azblob`](providers/azblob) | Via notifier + periodic reload | Event Grid → Service Bus | Blob document, ETag conditional reads |
| [`secretmanager`](providers/secretmanager) | Via notifier + periodic reload | Pub/Sub | Version/alias document, CRC32C verification |
| [`gcs`](providers/gcs) | Via notifier + periodic reload | Pub/Sub | Current object document, generation cache, generation-consistent reads |

“—” means no automatic change detection. Shared file watching requires
`WithWatch`; native provider watchers start with `New` independently of it.
Cloud providers refresh when `Reload` calls `Load`. In v1.6.0, register a consumer
with `WithNotifier` and schedule reconciliation with `WithResync`; Manager owns their
workers. AppConfig reloads also respect the service's next allowed poll time.

| Notifier module | Acknowledgment and failure behavior |
|---|---|
| [`integrations/notify/sns`](integrations/notify/sns) | SNS envelopes or raw messages through SQS; delete after successful refresh; failed refresh remains available for redelivery |
| [`integrations/notify/azservicebus`](integrations/notify/azservicebus) | Event Grid schema through Service Bus PeekLock; Complete after successful refresh, Abandon on failure |
| [`integrations/notify/pubsub`](integrations/notify/pubsub) | GCS / Secret Manager events; Ack after successful refresh, Nack on failure |

All three run a synchronous refresh callback with a bounded context. With v1.6.0,
Manager restarts a stopped consumer with backoff and jitter, stops it on shutdown,
and periodically reloads to recover missed notifications:

```go
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithProvider(remote),
    fastconf.WithNotifier(consumer),
    fastconf.WithResync(time.Minute),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Here `remote` and `consumer` are application-created provider and consumer values;
import `time` for the interval. `Load` starts no workers and ignores these options.
The application owns SDK clients and provisions a separate queue/subscription for
each independent replica that must receive every change.
See [cloud notifications](docs/cookbook/sources/cloud-notifications.md) for routing,
retries, dead letters and periodic reconciliation. `s3events` is deprecated in v1.3.0; use
[`integrations/notify/sns`](integrations/notify/sns). Its existing Watch
uses best-effort EventBridge → SQS notification and acknowledges before Manager
reload completes; use the new consumer for refresh-before-ack behavior.

See [embedded files and cloud sources](docs/cookbook/sources/cloud.md) for loading
and limits. Cloud SDKs live in independent modules. Except for the S3 loader, the
cloud loaders and all three notifier modules above were added in v1.2.0.

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
In v1.6.0, KV strings also become scalar lists and named numeric/boolean types:
`"a, b"` or `'["a","b"]'` can fill `[]string`, and `"3"` can fill `type Level int`.
Contiguous index maps such as `{"0":"a","1":"b"}` also become lists.
`[]byte` retains base64 semantics and custom unmarshalers retain their wire forms.

The v1.6.0 type fragment below imports `time`.

```go
type Database struct {
    DSN     string        `json:"dsn" fc:"required,secret" desc:"Database connection"`
    Pool    int           `json:"pool" default:"10" fc:"min=1,max=100"`
    Timeout time.Duration `json:"timeout" default:"5s"`
    Hosts   []string      `json:"hosts" default:"primary,replica"`
    Enabled bool          `json:"enabled" default:"true"`
}
```

In v1.6.0, `default:"…"` fills absent keys before typed decoding: `5s` becomes a
`time.Duration`, `primary,replica` becomes a list, and explicit false, 0, empty
strings are retained, while null counts as present. `Explain` records injected
defaults as `LayerDefault`.
Legacy `fc:"default=…"` still fills decoded Go zero values; `Defaulter.Defaults`
provides computed defaults. Field constraints run before `WithValidate` and
`WithPolicy`; policy warnings allow publication and error-severity findings reject it. Tags do not validate
collection elements; use a validator for those checks.
For pointers, `required` checks for nil; `min`, `max` and `oneof` check the
pointed-to value and skip nil pointers.

For interpolation in v1.6.0, add this option to `Load` or `New` (import
`github.com/fastabc/fastconf/transform`):

```go
fastconf.WithTransform(transform.Interpolate(transform.InterpolateOptions{Refs: true}))
```

```yaml
server:
  base_url: "https://${HOST:example.com}"
oauth:
  callback_url: "${server.base_url}/oauth/callback"
dsn: "postgres://${DB_USER:app}@${DB_HOST:localhost}/app"
```

`${VAR:default}` supplies an env fallback; `${VAR:?message}` requires a non-empty
value. Field references resolve recursively with cycle/depth checks. Env values
are not expanded twice. Put defaults needed by references in a base layer;
literal `default` tags are injected after interpolation and secret resolution.

In v1.6.0, `WithSecretResolvers` chains resolvers in registration order; the first
recognizer handles a reference. `PrefixSecretResolver` wraps an application
function for prefixes such as `kms://`. Use `vault.NewResolver` for KV v2 field
references or the optional `secrets/age` adapter for age decryption. Resolved
values are automatically masked in diagnostic views. `fc:"secret"` and
`WithSecretPaths` also mask sensitive inputs that do not use a resolver.
`Get()`, `Value()` and explicit `Unredacted()` views retain plaintext access.
See [field metadata](docs/cookbook/processing/field-meta.md), [typed hooks](docs/cookbook/processing/typed-hooks.md),
[interpolation](docs/cookbook/processing/interpolation.md), [policies](docs/cookbook/processing/policy.md)
and [secrets](docs/cookbook/processing/secrets.md).

### Generate configuration documentation (v1.6.0)

Use `schema.JSONSchema`, `schema.ExampleYAML` and `schema.ExampleEnv` to generate a
Draft 2020-12 schema, annotated YAML and env examples from a config struct.
`desc`, `default` and `fc` metadata supply descriptions, defaults, required fields,
constraints and secret markers. Run generation through `go generate` so application
config types and checked-in examples share one source. The reverse direction,
YAML to Go structs, remains available through `fastconfgen`.
See the [generation guide](docs/cookbook/processing/schema.md) and
[runnable schema example](examples/schema/README.md).

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

Subscriptions compare a selected scalar or entire subtree after publication.
For example, subscribe to Database and Log separately so a log-level change does
not reconnect the database; `WithEqual` can narrow equality to the DSN.
Callbacks run on the writer, must return quickly and must not wait on
that manager's reload/Plan/rollback/shutdown. Signal a bounded worker for costly
resource updates. An override applies to one reload and does not edit sources.

`Plan(ctx)` returns `Proposed`, `Diff`, `Validators` and `Policies` without
publishing. Inspect validator errors and policy severity even when Plan returns
nil error. Reload reads inputs again. `Pause` suppresses watcher triggers;
`Resume` does not replay ignored events, so reload explicitly after a paused batch.
In v1.6.0, pause also skips notifier-triggered refresh and periodic resync.

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
go install github.com/fastabc/fastconf/cmd/fastconfctl@v1.5.0
go install github.com/fastabc/fastconf/cmd/fastconfd@v1.5.0
go install github.com/fastabc/fastconf/cmd/fastconfgen@v1.5.0
fastconfctl dump -dir conf.d -profile prod -format yaml
fastconfctl diff -dir conf.d -from dev -to prod -json
fastconfctl validate -dir conf.d -profile prod
fastconfctl explain -dir conf.d -profile prod server.port
fastconfgen -in conf.d/base/00-app.yaml -pkg config -type Config
```

`fastconfctl validate` loads a generic map; it does not validate your application's
struct tags or business policies. Starting in v1.2.0, CLI diagnostic
output masks common credential keys by default. For application structs, use
`fc:"secret"` or `WithSecretPaths` for sensitive inputs; v1.6.0 also masks
resolver-produced values automatically.
See [diagnostics](docs/cookbook/runtime/introspect.md).

In v1.3.0, an empty diff `-from` selects base only, ignoring
`APP_PROFILE` and metadata defaults. fastconfd exits if its listen address is busy.

conf.d discovery accepts regular files (following symlinks), up to 4 MiB each,
including metadata; see [discovery limits](docs/design/spec.md#discovery-limits)
(v1.3.0).

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

- [Custom providers](docs/cookbook/sources/custom-provider.md): public helpers and conformance tests.

## License

[MIT](LICENSE) · Copyright (c) 2026 FastAbc
