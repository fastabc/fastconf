# 01 — Quickstart

## Quick start

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/provider"
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
        fastconf.WithProfile(fastconf.ProfileOptions{
            EnvVar:  "APP_PROFILE",
            Default: "dev",
        }),
        fastconf.WithProvider(provider.NewEnv("APP_")),
        fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
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
      _patch.json
```

Example base file:

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

`APP_DATABASE_POOL=20` maps to `database.pool` (single `_` is the default
separator, Viper / Spring Boot style — switch to `__` via
`provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)`
when keys must carry literal underscores). The external label
`server.addr=:9090` maps to `server.addr`. With the example above, env
overrides the file value for `database.pool`, and labels override the file
value for `server.addr`.

With `APP_PROFILE=prod`, FastConf merges `base/*` first, then
`overlays/prod/*`. The default decode bridge does a JSON round-trip, so if
your structs only carry `yaml` tags either add `json` tags or pass
`fastconf.WithCodecBridge(fastconf.BridgeYAML)` explicitly.

### Three recommended entry points

| Scenario | Recommended combo | Read next |
|---|---|---|
| Local file config, single service | `New + WithDir + Get` | `ExampleNew` / `docs/cookbook/introspect.md` |
| Kubernetes hot-reload | `PresetK8s + Subscribe + Errors` | `docs/cookbook/k8s.md` / `docs/cookbook/reload-policy.md` |
| Remote source / GitOps | `WithProvider + Plan + Provenance` | `docs/cookbook/vault.md` / `docs/cookbook/consul.md` / `docs/cookbook/plan.md` |

For unit tests use `PresetTesting`; for sidecars `PresetSidecar`; for
region / zone / host axis overlays see `PresetHierarchical` and
`WithMultiAxisOverlays`.

---

## Why FastConf

- **Strong typing on the read path.** `mgr.Get().Server.Addr` is checked
  by the compiler. No dotted-path strings, no reflection, no `interface{}`.
- **Lock-free hot reads.** `Get()` is an `atomic.Pointer.Load()` — O(1),
  zero-alloc, safe from any number of goroutines.
- **Fail-safe reload.** Any pipeline stage that errors out keeps the old
  `*State[T]` live; a broken config never reaches your read path.
- **Kustomize-style layering.** base / overlays, RFC 6902 patches, and
  policy-based `mergeKeys` strategic merge for lists of objects.
- **Opt-in extensions.** Providers, transformers, secret resolvers,
  validators, policies, metrics, and tracing are all optional.
- **Boundary-honest interface surface.** Public contracts live under
  `contracts/`; reusable primitives live under `pkg/*`; private helpers
  under `internal/*`; CI enforces dependency direction.

---

## Coming from another config library

Quick translation table for the most common idioms.

| Your library | Their idiom | FastConf equivalent | Caveat |
|---|---|---|---|
| **spf13/viper** | `viper.BindPFlag(...)` | `provider.NewCLI(cliadapter_pflag.FromChanged(cmd.Flags()))` | `BindPFlag` leaks pflag **defaults** into config; FastConf only forwards flags whose `Changed` bit is set. |
| **spf13/viper** | precedence (override > flag > env > config > kv > default) | `Priority*` constants: `PriorityDotEnv=5` → `PriorityCLI=60`, 7 explicit bands | DotEnv and K8s are first-class bands; precedence is set per-provider, not globally. |
| **knadh/koanf** | `k.Load(provider, parser)` — last load wins | `mgr.Add(provider)` + each provider's `Priority()` | Load order is **irrelevant**; priority alone decides. Reorder freely. |
| **knadh/koanf** | `koanf.WithMergeFunc(...)` | `pkg/merger` strategy + `policy/*` sub-modules | Strategy-driven merge (RFC 6902, mergeKeys, etc.), configured via options. |
| **kelseyhightower/envconfig** | `envconfig.Process("APP", &cfg)` | `provider.NewEnv("APP_")` | Prefix-based provider, not struct-tag scanner. CamelCase auto-split (`split_words`) is **not** supported — write the dotted key. |
| **kelseyhightower/envconfig** | `default:"foo"` tag | `merger.Defaults` layer (or struct zero value) | Defaults live in a dedicated layer, not in tags. |
| **kelseyhightower/envconfig** | `required:"true"` tag | `pkg/validate.Required(...)` | Validation is its own pipeline stage; runs after merge. |
| **caarlos0/env** | `envExpand` (`${VAR}` interpolation) | `transform.EnvSubst()` (process env) or `transform.EnvSubstWith(lookup func(string) string)` (custom) | Explicit transformer; supply a lookup closure to consult dotenv before `os.Getenv`. |
| **joho/godotenv** | `godotenv.Load(".env")` | `provider.NewDotEnv("APP_", ".env")` at `PriorityDotEnv=5` | **No `os.Setenv` mutation** — `.env` is a layer, not a side effect. Process env still overrides (presence-based, so `APP_PORT=""` also suppresses). |
| **joho/godotenv** | `godotenv.Overload(".env")` (force override) | `provider.NewDotEnv(...).WithPriority(contracts.PriorityCLI)` | Priority knob replaces the dual API. |
| **spf13/cobra + pflag** | `cmd.Flags()` | `cliadapter_pflag.FromChanged(cmd.Flags())` → `provider.NewCLI(...)` | Sub-module `github.com/fastabc/fastconf/integrations/cli/pflag` — keeps pflag out of the root module's dependency closure. |
| **stdlib `flag`** | `flag.FlagSet` | `cliadapter.FromStdFlag(fs)` → `provider.NewCLI(...)` | Zero-dep; lives in `pkg/cliadapter`. |
| **alecthomas/kong** / **urfave/cli** | typed flag struct / `cli.Context` | use `cliadapter.From(visit)` with a one-line visit closure | Pattern: walk only `Changed` / `IsSet` flags and call `yield(name, value)`. |

### Side-by-side: flag binding without the default-leak footgun

The single most common Viper bug is `BindPFlag` happily forwarding the
flag's **default** value into config even when the user never typed the
flag — silently overriding values you set in YAML or env. FastConf splits
the two concerns:

```go
// Viper (footgun-prone):
//   pflag default ("8080") wins over server.port: 9090 in app.yaml
viper.BindPFlag("server.port", cmd.Flags().Lookup("server.port"))

// FastConf (changed-only by construction):
//   only set if --server.port was explicitly typed
import cliflag "github.com/fastabc/fastconf/integrations/cli/pflag"

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(provider.NewCLI(cliflag.FromChanged(cmd.Flags()))),
)
```

### Side-by-side: env binding without struct-tag scanning

```go
// envconfig (struct-tag scanner, one-shot):
type Cfg struct {
    DSN  string `envconfig:"DATABASE_DSN" required:"true" default:"sqlite:///tmp/db"`
    Port int    `envconfig:"SERVER_PORT"  default:"8080"`
}
_ = envconfig.Process("APP", &cfg)

// FastConf (provider layer + dedicated defaults & validate):
type Cfg struct {
    Database struct{ DSN  string } // populated by env: APP_DATABASE_DSN
    Server   struct{ Port int }    // populated by env: APP_SERVER_PORT
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDefaults(Cfg{ /* zero-value or filled defaults */ }),
    fastconf.WithProvider(provider.NewEnv("APP_")),    // _ → . relaxed binding
    fastconf.WithValidate(validate.Required("Database.DSN")),
)
```

---

## Installation

```bash
go get github.com/fastabc/fastconf@latest

# Optional sub-modules:
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/cue@latest           # CUE validation + policy
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/providers/s3@latest
go get github.com/fastabc/fastconf/validate/playground@latest
```

Command-line tools (Go ≥ 1.22):

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

Each GitHub Release also ships prebuilt binaries for
`linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and `windows/amd64` with
`SHA256SUMS`.

---

