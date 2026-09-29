# FastConf — strongly typed, lock-free, Kustomize-style configuration for Go

> **Language**: English · [中文](README.zh.md)

`fastconf` layers YAML / JSON / TOML files, environment variables, CLI
flags, remote KV stores, and on-the-fly generators into a single strongly
typed Go struct. A single-writer reload loop publishes new snapshots atomically
via `atomic.Pointer`; the hot read path is one `atomic.Pointer.Load()`.

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **Status**: v1.0.0. This README describes
> the candidate API. See the [v1 migration guide](docs/cookbook/migration-v1.md)
> when upgrading from a published v0 release.

---

## Table of contents

1. [Quick start](#quick-start)
2. [Why FastConf](#why-fastconf)
3. [Installation](#installation)
4. [Core model](#core-model)
5. [Manager API](#manager-api)
6. [Reload pipeline](#reload-pipeline)
7. [Profiles & overlays](#profiles--overlays)
8. [Provider system](#provider-system)
9. [Transformers & migration](#transformers--migration)
10. [Watch, Subscribe, and Plan](#watch-subscribe-and-plan)
11. [Provenance, history & rollback](#provenance-history--rollback)
12. [Observability](#observability)
13. [Multi-tenant setups](#multi-tenant-setups)
14. [Sub-module ecosystem](#sub-module-ecosystem)
15. [CLI tools](#cli-tools)
16. [Performance](#performance)
17. [Development](#development)
18. [Documentation](#documentation)
19. [License](#license)

---

## Quick start

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/env"
)

type AppConfig struct {
    Server struct {
        Addr string `json:"addr" yaml:"addr"`
    } `json:"server" yaml:"server"`
    Database struct {
        DSN  string `json:"dsn"  yaml:"dsn"`
        Pool int    `json:"pool" yaml:"pool"`
    } `json:"database" yaml:"database"`
}

func main() {
    mgr, err := fastconf.New[AppConfig](context.Background(),
        fastconf.WithDir("conf.d"),
        fastconf.WithProfile(fastconf.Profile{
            Env:     "APP_PROFILE",
            Default: "dev",
        }),
        fastconf.WithProvider(env.NewEnv("APP_")),
        fastconf.WithWatch(fastconf.Watch{}),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer mgr.Close()

    cfg := mgr.Get() // *AppConfig — lock-free, O(1), zero-alloc
    log.Println(cfg.Server.Addr, cfg.Database.Pool)
}
```

Directory layout:

```text
conf.d/
  base/
    00-app.yaml
  overlays/
    prod/
      50-overrides.yaml
      90-fix.patch.json
```

```yaml
# conf.d/base/00-app.yaml
server:
  addr: ":8080"
database:
  dsn: "postgres://localhost/app"
  pool: 10
```

Run with an environment override:

```bash
APP_PROFILE=prod APP_DATABASE_POOL=20 go run .
```

`APP_DATABASE_POOL=20` maps to `database.pool` (single `_` separator,
Viper/Spring Boot style). With `APP_PROFILE=prod`, FastConf merges `base/*`
first, then `overlays/prod/*`.

---

## Why FastConf

- **Strong typing on the read path.** `mgr.Get().Server.Addr` is checked
  by the compiler. No dotted-path strings, no reflection, no `interface{}`.
- **Lock-free hot reads.** `Get()` is an `atomic.Pointer.Load()` — O(1),
  zero-alloc, safe from any number of goroutines.
- **Fail-safe reload.** Any pipeline stage that errors out keeps the old
  `*State[T]` live; a broken config never reaches your read path.
- **Kustomize-style layering.** base / overlays, RFC 6902 patches, and
  strategic merge for lists of objects.
- **Opt-in extensions.** Providers, transformers, secret resolvers,
  validators, policies, metrics, and tracing are all optional.

---

## Installation

Requires Go 1.24+ for the root module. Submodules require at least Go 1.24;
those with newer dependencies declare a higher minimum in their go.mod.
Version **v1.0.0 is prepared but not tagged**. Build this checkout to use it:

```bash
go build ./...
go build ./cmd/fastconfctl ./cmd/fastconfd
```

After publication:

```bash
go get github.com/fastabc/fastconf@v1.0.0
go get github.com/fastabc/fastconf/integrations/log/zerolog@v1.0.0  # or .../log/phuslu
go get github.com/fastabc/fastconf/observability/metrics/prometheus@v1.0.0
go get github.com/fastabc/fastconf/observability/otel@v1.0.0
```

Root tags use `vX.Y.Z`; independent modules use `<module>/vX.Y.Z`.
All modules release together at the same version. See [releasing](RELEASING.md).

---

## Core model

```text
sources / generators / providers
              │
              ▼
       assemble preflight
              │
              ▼
 merge → transform → secret → typed-hooks
      → decode → field-meta → validate → policy
              │
      fail ───┴─── keep old State[T]
              │
           success
              ▼
 canonical hash → atomic swap → history → observers → subscribers
```

| Property | What it means |
|---|---|
| Typed read path | `mgr.Get().Server.Addr`, checked by the compiler |
| Single-writer reload | fsnotify, provider events, and manual `Reload` all serialize through one writer |
| Fail-safe | Any stage error keeps the old `*State[T]`; bad config never reaches business code |
| Kustomize-style layering | base / overlay, RFC 6902 patches, strategic merge with `mergeKeys` |
| Opt-in extensions | providers, transformers, secret resolvers, policies, metrics, tracer |

---

## Manager API

`New[T]` loads synchronously and starts watching; `Load[T]` loads once.
`Get()` returns a read-only `*T`, while `Snapshot()` adds diagnostics.
`Reload(ctx)` and `Plan(ctx)` serialize through the same writer. `Plan` reports
all validators (including successes) and policy findings without publishing.
Call `Close()` when finished; use `Shutdown(ctx)` to bound the wait.
See the [API reference](https://pkg.go.dev/github.com/fastabc/fastconf).

---

## Reload pipeline

Assembly scans files and loads generators and providers, then the stages above
merge, transform, resolve secrets, decode, validate and evaluate policy.
Failures preserve the current state and generation, reach `Errors()`, and emit
`ReloadFinished{Err}` without `Committed`. A successful change hashes the typed
value, publishes it atomically, retains history and notifies observers and
subscribers. See the [runtime contract](docs/design/spec.md).

---

## Profiles & overlays

Files in `base/` apply first, followed by matched `overlays/<profile>/`.
`WithProfile(Profile{Names: []string{"prod", "eu-west"}})` selects multiple
profiles; each overlay's `_meta.yaml.match` supports `&`, `|`, `!`, and parentheses.
Root `_meta.yaml` configures defaults and merge behavior. Files ending in
`.patch.json` apply RFC 6902 operations.

`WithAxes` adds independent overlay dimensions. Higher `Axis.Priority` wins
across whole directories; declaration order breaks ties, then filenames order
files. Up to 40 axes, 100 files per overlay, and 1000 base files are supported.
See the [runtime contract](docs/design/spec.md).

---

## Provider system

### Built-in structured providers (`providers/*`)

| Provider | Constructor | Notes |
|---|---|---|
| Env | `env.NewEnv("APP_")` (`providers/env`) | `APP_FOO_BAR` → `foo.bar`; chain `.WithReplacer`, `.At`, `.WithCoerce` |
| CLI | `cliflag.NewCLI(map)` (`providers/cliflag`) | Pass only explicitly changed flags; files/env stay authoritative |
| DotEnv | `dotenv.NewDotEnv("APP_", paths...)` (`providers/dotenv`) | `.env` fallback; process env wins |
| Labels | `labels.New(labels, labels.Options{})` (`providers/labels`) | Dotted config labels; `Options.Routing` enables the routing DSL |
| K8s Downward | `k8s.NewDefault()` | `/etc/podinfo/{labels,annotations}` |

First-party KV providers (root module; only imported packages are linked):

```go
vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
```

NATS and Redis Streams reference implementations live in `examples/nats` and
`examples/redisstream`; copy and adapt them with a real client. S3 remains an
independent provider module (`providers/s3`). HTTP supports automatic codec
selection with a nil codec and manual-only fetching with `WithInterval(0)`.

Provider priorities, low to high: DotEnv (5), Static (10), KV (30),
K8s (40), Env (50), CLI (60). Equal priorities follow registration order.
Implement `contracts.Provider` (`Name`, `Load`, `Watch`); optional
`contracts.Describer` supplies priority and file-watch paths.

---

## Transformers & migration

```go
fastconf.WithTransform(
    transform.EnvSubst(),
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{"db.url": "database.dsn"}),
)
fastconf.WithMergeKeys(map[string]string{"listeners": "name"})
```

Transforms run after merging. Keyed lists merge through `WithMergeKeys` or
`_meta.yaml` before this stage. Defaults belong in `fc:"default=…"` tags or
`Defaulter.Defaults`; `fc:"secret"` marks sensitive fields. `transform.New`
builds schema migration chains. See [migration](docs/cookbook/migration-v1.md)
and [secrets](docs/cookbook/secrets.md).

---

## Watch, Subscribe, and Plan

```go
cancel := fastconf.Subscribe(mgr,
    func(c *AppConfig) *string { return &c.Database.DSN },
    func(old, next *string) { reconnect(*next) },
)
defer cancel()

err := mgr.Reload(ctx, fastconf.WithReason("admin"),
    fastconf.WithOverride(map[string]any{"enabled": true}))
result, err := mgr.Plan(ctx, fastconf.WithPlanHostname("prod-1"))
```

Subscribe compares extracted values; `WithEqual` customizes equality.
Override applies only to that reload. Plan returns proposed state, diff,
validator results and policy findings. `Pause()` ignores file and provider
change events; `Resume()` resumes listening without replaying ignored events.
Call `Reload(ctx)` explicitly after a batch to load its final state. See [subscriptions](docs/cookbook/observer.md) and
[plans](docs/cookbook/plan.md).

---

## Provenance, history & rollback

Enable `WithProvenance(ProvenanceFull)` for per-leaf origin chains via
`mgr.Snapshot().Explain("server.addr")`; `ProvenanceTopLevel` tracks top-level
keys and `ProvenanceOff` disables tracking. Secret list paths use `items.0`.

`WithHistory(10)` retains prior snapshots. `mgr.History().List()` is ordered
oldest first; pass a retained snapshot to `Rollback`. Zero disables history;
negative capacities are errors. Failed reloads reach `mgr.Errors()`.
Snapshot `Map`, `Dump`, `Diff`, and `Explain` mask secrets; use `Unredacted()`
only where plaintext is intended. See [history](docs/cookbook/introspect.md).

---

## Observability

`WithObserver` receives reload, stage, provider-error, dropped-event and commit
notifications. Compose observers using `observe.Multi`, `observe.JSONLines`,
`observe.Metrics` and `observe.Func`. Slow observers can use `observe.Async`;
close the manager before closing the async observer to drain queued events.

Prometheus (`observability/metrics/prometheus`) and OpenTelemetry
(`observability/otel`) remain separate modules. Logging adapters
(`integrations/log/phuslu`, `integrations/log/zerolog`) are separate modules
that share a dependency-free logging core in the root module, so each pulls in only its own backend.
See [observability](docs/cookbook/observability.md) and [logging](docs/cookbook/log.md).

---

## Multi-tenant setups

```go
// Multi-tenant: each tenant is a fully isolated Manager[T] tagged with its id;
// keep them in your own registry (see docs/cookbook/tenant.md).
mgrA, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("/etc/config/tenant-a"),
    fastconf.WithTenant("tenant-a"),
)
```

---

## Sub-module ecosystem

### Shipped with the root module

| Package | Path |
|---|---|
| contracts | `contracts` — public interfaces |
| reusable primitives | `codec`, `confmap`, `transform`, `feature`, `providers/{env,cliflag,dotenv,labels,source}` |
| http / vault / consul | `providers/{http,vault,consul}` |
| policy | `policy` — `Func` adapter |
| sidecar service | `cmd/fastconfd` |
| CLI tools | `cmd/fastconfctl` (root), `cmd/fastconfgen` (own module) |
| integrations | `integrations/render` |

### Independent sub-modules (`go get` as needed)

| Sub-module | Path | Primary dependency |
|---|---|---|
| validate/playground | `validate/playground` | go-playground/validator |
| Prometheus | `observability/metrics/prometheus` | Prometheus |
| OpenTelemetry | `observability/otel` | OpenTelemetry |
| phuslu adapter | `integrations/log/phuslu` | phuslu/log |
| zerolog adapter | `integrations/log/zerolog` | zerolog |
| generator | `cmd/fastconfgen` | yaml.v3 |
| cue (validation + policy) | `cue` | cuelang.org/go |
| opa-policy | `policy/opa` | open-policy-agent/opa |
| cli/pflag | `integrations/cli/pflag` | spf13/pflag |
| s3 provider | `providers/s3` | AWS SDK v2 |

Tag every module at the same version: `./tools/tag-release.sh vX.Y.Z [--push]`

---

## CLI tools

### `fastconfd` — sidecar service

```bash
fastconfd --dir=/etc/config --profile=prod --addr=127.0.0.1:8081 \
  --read-token="$FASTCONFD_READ_TOKEN" --reload-token="$FASTCONFD_RELOAD_TOKEN"
```

| Endpoint | Method | Description |
|---|---|---|
| `/healthz` | GET  | Plain text `ok` once the first reload succeeded |
| `/version` | GET  | Version, generation, hash, load time, reason |
| `/config`  | GET  | Current config JSON, secrets masked; `?unredacted=true` + `X-Unredacted-Token` for plaintext |
| `/dump`    | GET  | Deterministic YAML (`?format=json` for JSON) |
| `/reload`  | POST | Trigger a manual reload |
| `/events`  | GET  | SSE stream of `ReloadCause` when a new snapshot commits |

`FASTCONFD_READ_TOKEN` and `FASTCONFD_RELOAD_TOKEN` must be non-empty.
`/config`, `/dump`, and `/events` require `X-Config-Token`; `/reload` requires
`X-Reload-Token`. See the [sidecar recipe](docs/cookbook/sidecar.md).

### `fastconfctl` — admin CLI

```bash
fastconfctl dump     -dir conf.d -profile prod
fastconfctl diff     -dir conf.d -from dev -to prod --json
fastconfctl validate -dir conf.d -profile prod
fastconfctl explain  -dir conf.d -profile prod database.dsn
```

### `fastconfgen` — code generator

```bash
fastconfgen -in conf.d/base/00-app.yaml -pkg config -type Config -out config/config_gen.go
```

---

## Performance

Performance contracts and reproducible benchmark commands are maintained in
[Performance Notes](docs/design/perf.md). Use `tools/profile-reload.sh` to measure your target
machine; `tools/bench-guard.sh` checks the read-path latency and zero-allocation budget.

---

## Development

```bash
go mod tidy
make build
make test        # go test -race -count=1 ./...
make test-workspace # all modules against this checkout
make test-candidate # isolated dependency resolution before publication
make lint        # requires golangci-lint
make check       # release tooling regression tests

go test ./... -run '^Example' -v
go test -bench=BenchmarkGet -benchmem ./...
```

---

## Documentation

| Doc | Purpose |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Source responsibilities, package boundaries, test layout and naming |
| [docs/cookbook/README.md](docs/cookbook/README.md) | Ready recipes ordered by user journey |
| [docs/design/spec.md](docs/design/spec.md) | Runtime model, concurrency, module boundaries |
| [docs/cookbook/migration-v1.md](docs/cookbook/migration-v1.md) | v0 → v1 migration guide |
| [docs/cookbook/migration-v0.md](docs/cookbook/migration-v0.md) | Archived notes for moves between v0 releases |
| [GitHub Releases](https://github.com/fastabc/fastconf/releases) | Release notes and prebuilt CLI binaries |
| [pkg.go.dev](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc and runnable examples |

Common recipes: [k8s](docs/cookbook/k8s.md) · [vault](docs/cookbook/vault.md) ·
[consul](docs/cookbook/consul.md) · [secrets](docs/cookbook/secrets.md) ·
[features](docs/cookbook/features.md) · [policy](docs/cookbook/policy.md) ·
[otel](docs/cookbook/observability.md#opentelemetry) · [tenant](docs/cookbook/tenant.md) ·
[sidecar](docs/cookbook/sidecar.md) · [plan](docs/cookbook/plan.md)

## License

MIT License, See [`LICENSE`](LICENSE).

Copyright (c) 2026 FastAbc
