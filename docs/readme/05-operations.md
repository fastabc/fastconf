# 05 — Operations

## Sub-module ecosystem

### Shipped with the root module (same version, regular import)

| Package | Path | Notes |
|---|---|---|
| contracts | `contracts` | Public interfaces: Provider / Codec / Source / Event |
| pkg/* | `pkg/{decoder,discovery,feature,flog,generator,mappath,merger,migration,profile,provider,transform,validate}` | Reusable primitives |
| internal/* | `internal/{debounce,obs,typeinfo,watcher}` | Compile-time API boundary |
| http        | `providers/http`   | HTTP / SSE provider (build tag `no_provider_http`) |
| vault       | `providers/vault`  | HashiCorp Vault KV v2 (build tag `no_provider_vault`) |
| consul      | `providers/consul` | Consul KV (build tag `no_provider_consul`) |
| policy      | `policy`           | Policy interface + Func adapter |
| integrations/bus | `integrations/bus` | Configuration change bus |
| integrations/render | `integrations/render` | Template render extension |
| cmd/fastconfd | `cmd/fastconfd`  | Sidecar HTTP + SSE service |

### Independent sub-modules (`go get` as needed)

| Sub-module | Path | Tag prefix | Primary dependency |
|---|---|---|---|
| validate/playground | `validate/playground` | `validate/playground/vX.Y.Z` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | `observability/metrics/prometheus/vX.Y.Z` | prometheus/client_golang |
| otel | `observability/otel` | `observability/otel/vX.Y.Z` | OpenTelemetry SDK |
| cue (unified) | `cue` | `cue/vX.Y.Z` | cuelang.org/go (CUE validation + policy) |
| opa-policy | `policy/opa` | `policy/opa/vX.Y.Z` | open-policy-agent/opa |
| log/phuslu | `integrations/log/phuslu` | `integrations/log/phuslu/vX.Y.Z` | phuslu/log |
| log/zerolog | `integrations/log/zerolog` | `integrations/log/zerolog/vX.Y.Z` | rs/zerolog |
| cli/pflag | `integrations/cli/pflag` | `integrations/cli/pflag/vX.Y.Z` | spf13/pflag |
| nats provider | `providers/nats` | root-versioned (`vX.Y.Z`) | root module only (caller injects `nats.Conn`) |
| redis-streams provider | `providers/redisstream` | root-versioned (`vX.Y.Z`) | root module only (caller injects redis client) |
| openfeature | `integrations/openfeature` | root-versioned (`vX.Y.Z`) | root module only |
| s3 provider | `providers/s3` | `providers/s3/vX.Y.Z` | AWS SDK v2 (load + ETag short-circuit, `FromURL` helper) |
| s3events provider | `providers/s3/s3events` | root-versioned via `providers/s3` | AWS SDK v2 SQS (EventBridge S3 → SQS watch, subpackage of s3) |
| cmd/fastconfctl | `cmd/fastconfctl` | `cmd/fastconfctl/vX.Y.Z` | root module only |
| cmd/fastconfgen | `cmd/fastconfgen` | `cmd/fastconfgen/vX.Y.Z` | yaml.v3 |

Tag every sub-module at once via `tools/tag-release.sh`:

```bash
./tools/tag-release.sh vX.Y.Z          # local tags only
./tools/tag-release.sh vX.Y.Z --push   # push and trigger release.yml
./tools/tag-release.sh vX.Y.Z --force --push
./tools/tag-release.sh vX.Y.Z --delete --push
```

---

## Extension guide

### Custom Provider

```go
type RedisProvider struct {
    client *redis.Client
    key    string
    ch     chan contracts.Event
}

func (p *RedisProvider) Name()     string { return "redis:" + p.key }
func (p *RedisProvider) Priority() int    { return contracts.PriorityKV }

func (p *RedisProvider) Load(ctx context.Context) (map[string]any, error) {
    raw, err := p.client.Get(ctx, p.key).Bytes()
    if err != nil { return nil, err }
    var out map[string]any
    return out, json.Unmarshal(raw, &out)
}

func (p *RedisProvider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
    go p.watchLoop(ctx)
    return p.ch, nil
}

func init() {
    fastconf.RegisterProviderFactory("redis", func(cfg map[string]any) (contracts.Provider, error) {
        return NewRedisProvider(cfg["addr"].(string), cfg["key"].(string))
    })
}
```

### Custom Transformer

```go
type PrefixTransformer struct{ Prefix string }

func (t PrefixTransformer) Name() string { return "prefix:" + t.Prefix }
func (t PrefixTransformer) Transform(root map[string]any) error {
    if v, ok := root["app_name"].(string); ok {
        root["app_name"] = t.Prefix + "-" + v
    }
    return nil
}

fastconf.WithTransformers(PrefixTransformer{Prefix: "myorg"})
```

### Custom Codec

YAML, JSON, and TOML are registered automatically. Register a new
format like this:

```go
fastconf.RegisterCodec("hcl", hclCodec{})
fastconf.RegisterCodecExt("hcl", "hcl") // .hcl files route to "hcl"
```

### Picking an extension point

| Need | Use |
|---|---|
| Add a data source | implement `contracts.Provider` |
| Rewrite the merged tree | implement `Transformer` |
| Decrypt leaves before decode | implement `SecretResolver` |
| Type-rewrite leaves before decode | implement `decoder.TypedHook` |
| Assert after decode | `WithValidator` / `WithPolicy` |
| Act on successful publish | `AuditSink` / `DiffReporter` |
| Add a file format | implement `contracts.Codec` + `RegisterCodec` |

---

## CLI tools

### `fastconfd` — sidecar service

```bash
fastconfd --dir=/etc/config --profile=prod --addr=:8081
```

| Endpoint | Method | Description |
|---|---|---|
| `/healthz` | GET  | `{"status":"ok","generation":N}` |
| `/version` | GET  | Current state version (Hash + Generation) |
| `/config`  | GET  | Current config JSON (secrets redacted) |
| `/reload`  | POST | Trigger a manual reload; accepts `{"request_id":"…"}` |
| `/events`  | GET  | SSE stream of `ReloadCause` JSON on every successful reload |

### `fastconfctl` — admin CLI

```bash
fastconfctl snapshot --addr=:8081
fastconfctl reload   --addr=:8081 --request-id=deploy-123
fastconfctl plan     --addr=:8081
fastconfctl rollback --addr=:8081 --generation=42
fastconfctl sources  --addr=:8081
```

### `fastconfgen` — code generator

```bash
fastconfgen generate --input=conf.d/base/00-app.yaml --pkg=config --out=config/config_gen.go
```

---

## Performance

Most recent benchmark run: **Apple M2 / darwin-arm64 / Go 1.26.2**.

| Benchmark | median |
|---|---:|
| `BenchmarkGet` | 0.52 ns/op |
| `BenchmarkReloadNoop` | 15.1 µs/op |
| `BenchmarkReloadCommitSmall` | 16.5 µs/op |
| `BenchmarkReloadManySubscribers/50` | 17.5 µs/op |
| `BenchmarkIntrospectCold` | 1.67 µs/op |
| `BenchmarkExplainDeep` | 219 ns/op |

Full baseline, command lines, and explanation: [`docs/design/perf.md`](../design/perf.md).

The contract is: **hot reads are essentially free; reload may fail but
never publishes a half-built state; subscriber fan-out never blocks the
read path.**

---

## Development

```bash
# Dependencies
go mod tidy

# Build / test / lint
make build
make test         # go test -race -count=1 ./...
make test-all     # includes sub-modules
make lint         # requires golangci-lint

# Examples
go test ./... -run '^Example' -v

# Benchmarks
go test -bench=BenchmarkGet -benchmem ./...

# CI guards
bash tools/check-layout.sh
bash tools/check-doc-symbols.sh
bash tools/check-deps.sh
bash tools/bench-guard.sh
bash tools/loc-budget.sh
bash tools/total-loc-budget.sh

# Code-review dependency graph
bash tools/code-review-graph.sh
```

---

## Documentation map

| Doc | Purpose |
|---|---|
| [`docs/cookbook/README.md`](../cookbook/README.md) | Single entry point for every recipe |
| [`docs/design/spec.md`](../design/spec.md) | Runtime model, concurrency, module boundaries |
| [`docs/design/perf.md`](../design/perf.md) | Latest benchmark baseline |
| [`CHANGELOG.md`](../../CHANGELOG.md) | Release notes |
| [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc and runnable examples |

Common recipes:

- [`k8s`](../cookbook/k8s.md) · [`reload-policy`](../cookbook/reload-policy.md) · [`plan`](../cookbook/plan.md)
- [`vault`](../cookbook/vault.md) · [`consul`](../cookbook/consul.md) · [`cross-process`](../cookbook/cross-process.md) · [`provider-timeouts`](../cookbook/provider-timeouts.md)
- [`secrets`](../cookbook/secrets.md) · [`features`](../cookbook/features.md) · [`openfeature`](../cookbook/openfeature.md)
- [`diff-reporter`](../cookbook/diff-reporter.md) · [`policy`](../cookbook/policy.md) · [`otel`](../cookbook/otel.md)
- [`introspect`](../cookbook/introspect.md) · [`field-meta`](../cookbook/field-meta.md) · [`typed-hooks`](../cookbook/typed-hooks.md)
- [`labels`](../cookbook/labels.md) · [`strategic-merge`](../cookbook/strategic-merge.md) · [`generators`](../cookbook/generators.md)
- [`tenant`](../cookbook/tenant.md) · [`sidecar`](../cookbook/sidecar.md) · [`dump`](../cookbook/dump.md) · [`log`](../cookbook/log.md)

---

## License

MIT License, See [`LICENSE`](../../LICENSE).

Copyright (c) 2026 FastAbc
