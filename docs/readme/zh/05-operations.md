# 05 — 生产运维

## 为什么没有 `GetString("a.b.c")`

这是 FastConf 的边界，不是遗漏。

- 业务热路径应该走 `mgr.Get().Server.Addr`：强类型、零反射、零分配。
- CLI / dump / diff 这类动态场景，走 `state.Introspect().Keys()`、`Settings()`、
  `At(path)`。
- 如果你的配置天生没有稳定 schema，也可以直接使用
  `fastconf.New[map[string]any](...)`，只是会主动放弃类型安全。

详见 [`docs/cookbook/introspect.md`](../../cookbook/introspect.md)。

---

## 性能与可靠性

最近一次基准重测环境：**Apple M2 / darwin-arm64 / Go 1.26.2**。

| Benchmark | 中位数 |
|---|---:|
| `BenchmarkGet` | 0.52 ns/op |
| `BenchmarkReloadNoop` | 15.1 µs/op |
| `BenchmarkReloadCommitSmall` | 16.5 µs/op |
| `BenchmarkReloadManySubscribers/50` | 17.5 µs/op |
| `BenchmarkIntrospectCold` | 1.67 µs/op |
| `BenchmarkExplainDeep` | 219 ns/op |

完整基线、命令和解释见 [`docs/design/perf.md`](../../design/perf.md)。当前契约是：
**热读极轻、reload 可失败但不污染 live state、订阅 fan-out 不阻塞读取**。

---

## Sub-module 生态矩阵

### 主模块内置包（随根模块版本发布，`import` 路径不变）

| 包 | 路径 | 说明 |
|---|---|---|
| contracts | `contracts` | Provider / Codec / Source / Event 接口定义 |
| pkg/* | `pkg/{decoder,discovery,feature,flog,generator,mappath,merger,migration,profile,provider,transform,validate}` | 公开可复用实现原语 |
| internal/* | `internal/{coalesce,diffreport,fcerr,fctypes,manager,obs,options,pipeline,provenance,registry,secret,state,tenant,testutil,typeinfo,watcher}` | 编译时 API boundary 私有 helper |
| http        | `providers/http`   | HTTP / SSE Provider（build tag `no_provider_http`） |
| vault       | `providers/vault`  | HashiCorp Vault KV v2（build tag `no_provider_vault`） |
| consul      | `providers/consul` | Consul KV（build tag `no_provider_consul`） |
| nats provider | `providers/nats` | 调用方注入 `nats.Conn`；随根模块发布 |
| redis-streams provider | `providers/redisstream` | 调用方注入 redis client；随根模块发布 |
| policy      | `policy`           | Policy 接口 + Func adapter |
| integrations/bus | `integrations/bus` | 配置变更事件总线 |
| integrations/openfeature | `integrations/openfeature` | OpenFeature provider adapter |
| integrations/render | `integrations/render` | 模板渲染扩展 |
| cmd/fastconfd | `cmd/fastconfd`  | Sidecar HTTP + SSE 服务（与主模块同版） |
| cmd/fastconfctl | `cmd/fastconfctl` | 管理 CLI |
| cmd/fastconfgen | `cmd/fastconfgen` | struct 生成器 |

### 独立 Sub-module（按需 `go get`）

| Sub-module | 路径 | Tag prefix | 主要依赖 |
|---|---|---|---|
| validate/playground | `validate/playground` | `validate/playground/vX.Y.Z` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | `observability/metrics/prometheus/vX.Y.Z` | prometheus/client_golang |
| otel | `observability/otel` | `observability/otel/vX.Y.Z` | OpenTelemetry SDK |
| cue（统一） | `cue` | `cue/vX.Y.Z` | cuelang.org/go（CUE 验证 + 策略） |
| opa-policy | `policy/opa` | `policy/opa/vX.Y.Z` | open-policy-agent/opa |
| log/phuslu | `integrations/log/phuslu` | `integrations/log/phuslu/vX.Y.Z` | phuslu/log |
| log/zerolog | `integrations/log/zerolog` | `integrations/log/zerolog/vX.Y.Z` | rs/zerolog |
| cli/pflag | `integrations/cli/pflag` | `integrations/cli/pflag/vX.Y.Z` | spf13/pflag |
| s3 provider | `providers/s3` | `providers/s3/vX.Y.Z` | AWS SDK v2（load + ETag 短路，`FromURL` 辅助函数） |
| s3events provider | `providers/s3/s3events` | 随 `providers/s3` 版本 | AWS SDK v2 SQS（EventBridge S3 → SQS watch 子包） |

统一打 tag（`tools/tag-release.sh`）：

```bash
./tools/tag-release.sh vX.Y.Z          # 本地打全部 tag
./tools/tag-release.sh vX.Y.Z --push   # 同时推送（触发 release.yml）
./tools/tag-release.sh vX.Y.Z --force --push
```

---

## 扩展指南

### 自定义 Provider

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

### 自定义 Transformer

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

### 自定义 Codec

YAML、JSON、TOML 已在 `pkg/decoder` 的 `init` 中注册，**无需**重复
`RegisterCodec`。如需接入第三方格式（HCL、JSON5 等），按下例注册：

```go
fastconf.RegisterCodec("hcl", hclCodec{})
fastconf.RegisterCodecExt("hcl", "hcl") // 让 .hcl 扩展名走 "hcl" codec
```

### 选择扩展点

| 需求 | 选择 |
|---|---|
| 新增数据源 | 实现 `contracts.Provider` |
| 合并后改写树结构 | 实现 `Transformer` |
| decode 前解密 leaf | 实现 `SecretResolver` |
| decode 前类型重写 leaf | 实现 `decoder.TypedHook` |
| validate 后断言 | `WithValidator` / `WithPolicy` |
| 发布后动作 | `AuditSink` / `DiffReporter` |
| 新文件格式 | 实现 `contracts.Codec` + `RegisterCodec` |

---

## CLI 工具

### `fastconfd` — Sidecar 服务

```bash
fastconfd --dir=/etc/config --profile=prod --addr=:8081
```

| 端点 | 方法 | 说明 |
|---|---|---|
| `/healthz` | GET  | `{"status":"ok","generation":N}` |
| `/version` | GET  | 版本、generation、hash、加载时间、原因 |
| `/config`  | GET  | 当前配置 JSON；传 `?redact=true` 才脱敏 |
| `/dump`    | GET  | 确定性 YAML（`?format=json` 输出 JSON） |
| `/reload`  | POST | 触发手动 reload；配置 token 时要求 `X-Reload-Token` |
| `/events`  | GET  | SSE 流；每次成功 reload 推送 `ReloadCause` JSON |

### `fastconfctl` — 管理 CLI

```bash
fastconfctl dump     -dir conf.d -profile prod
fastconfctl diff     -dir conf.d -from dev -to prod --json
fastconfctl validate -dir conf.d -profile prod
fastconfctl explain  -dir conf.d -profile prod database.dsn
```

### `fastconfgen` — 代码生成器

```bash
fastconfgen -in conf.d/base/00-app.yaml -pkg config -type Config -out config/config_gen.go
```

---

## 本地开发

```bash
# 拉依赖
go mod tidy

# 构建 / 测试 / Lint
make build
make test         # 等价于 go test -race -count=1 ./...
make test-all     # 含 cmd/ 子模块
make lint         # 需要 golangci-lint

# Example 全跑
go test ./... -run '^Example' -v

# 性能基准
go test -bench=BenchmarkGet -benchmem ./...

# CI 防线
bash tools/check-layout.sh
bash tools/check-doc-symbols.sh
bash tools/check-deps.sh
bash tools/bench-guard.sh        # ns/op + allocs 阈值
bash tools/loc-budget.sh         # 主包 LOC 预算
bash tools/total-loc-budget.sh   # 全树 LOC 预算

# 代码评审依赖图
bash tools/code-review-graph.sh
```

---

## 文档地图

| 文档 | 用途 |
|---|---|
| [`docs/cookbook/README.md`](../../cookbook/README.md) | 所有 recipe 的单一入口 |
| [`docs/design/spec.md`](../../design/spec.md) | 运行模型、并发与模块边界 |
| [`docs/design/perf.md`](../../design/perf.md) | 最新 benchmark baseline |
| [`CHANGELOG.md`](../../../CHANGELOG.md) | 变更记录 |
| [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc 与 Example |

最常用的 recipe：

- [`k8s`](../../cookbook/k8s.md) · [`reload-policy`](../../cookbook/reload-policy.md) · [`plan`](../../cookbook/plan.md)
- [`vault`](../../cookbook/vault.md) · [`consul`](../../cookbook/consul.md) · [`cross-process`](../../cookbook/cross-process.md) · [`provider-timeouts`](../../cookbook/provider-timeouts.md)
- [`secrets`](../../cookbook/secrets.md) · [`features`](../../cookbook/features.md) · [`openfeature`](../../cookbook/openfeature.md)
- [`diff-reporter`](../../cookbook/diff-reporter.md) · [`policy`](../../cookbook/policy.md) · [`otel`](../../cookbook/otel.md)
- [`introspect`](../../cookbook/introspect.md) · [`field-meta`](../../cookbook/field-meta.md) · [`typed-hooks`](../../cookbook/typed-hooks.md)
- [`labels`](../../cookbook/labels.md) · [`strategic-merge`](../../cookbook/strategic-merge.md) · [`generators`](../../cookbook/generators.md)
- [`tenant`](../../cookbook/tenant.md) · [`sidecar`](../../cookbook/sidecar.md) · [`dump`](../../cookbook/dump.md) · [`log`](../../cookbook/log.md)

---

## License

MIT License, See [`LICENSE`](../../../LICENSE).

Copyright (c) 2026 FastAbc
