# FastConf — 强类型 · 无锁 · Kustomize 风格配置框架

> **语言**: [English](README.md) · 中文

`fastconf` 把 YAML / JSON / TOML、环境变量、命令行参数、远程 KV 与生成器
layer 叠加成一个强类型 Go 结构体，并在热更新时用单写者 reload loop 和
`atomic.Pointer` 安全发布不可变快照。业务读路径就是一次 `atomic.Pointer.Load()`，
并保持零分配。

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **Status**: first-public。当前 API 仍以"把语义收准"为第一目标；
> [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) 与本文档描述的是当前真相。

---

## 目录

1. [快速上手](#快速上手)
2. [为什么选 FastConf](#为什么选-fastconf)
3. [安装](#安装)
4. [核心模型](#核心模型)
5. [Manager API](#manager-api)
6. [Option 参考](#option-参考)
7. [Reload Pipeline](#reload-pipeline)
8. [Profile 与 Overlay](#profile-与-overlay)
9. [Provider 系统](#provider-系统)
10. [Transformer 与迁移](#transformer-与迁移)
11. [Watch、Subscribe 与 Plan](#watchsubscribe-与-plan)
12. [来源追溯、历史与回滚](#来源追溯历史与回滚)
13. [可观测性](#可观测性)
14. [多租户与预设](#多租户与预设)
15. [子模块生态](#子模块生态)
16. [CLI 工具](#cli-工具)
17. [性能](#性能)
18. [本地开发](#本地开发)
19. [License](#license)

---

## 快速上手

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

    cfg := mgr.Get() // *AppConfig — 无锁，O(1)，零分配
    log.Println(cfg.Server.Addr, cfg.Database.Pool)
}
```

目录结构示例：

```text
conf.d/
  base/
    00-app.yaml
  overlays/
    prod/
      50-overrides.yaml
      _patch.json
```

```yaml
# conf.d/base/00-app.yaml
server:
  addr: ":8080"
database:
  dsn: "postgres://localhost/app"
  pool: 10
```

使用环境变量覆盖运行：

```bash
APP_PROFILE=prod APP_DATABASE_POOL=20 go run .
```

`APP_DATABASE_POOL=20` 映射到 `database.pool`（单下划线分隔符，Viper/Spring Boot 风格）。
设置 `APP_PROFILE=prod` 后，FastConf 先合并 `base/*`，再合并 `overlays/prod/*`。

### 推荐入口

| 场景 | 推荐组合 | 深入阅读 | 可运行示例 |
|---|---|---|---|
| 本地文件配置 | `New + WithDir + Get` | [Quickstart](docs/readme/zh/01-quickstart.md) | [`examples/basic`](examples/basic/example_test.go) |
| Kubernetes 热更新 | `PresetK8s + Subscribe + Errors` | [k8s cookbook](docs/cookbook/k8s.md) | [`examples/sidecar`](examples/sidecar/example_test.go) |
| 远程配置 / GitOps | `WithProvider + Plan + Provenance` | [Vault](docs/cookbook/vault.md) / [Consul](docs/cookbook/consul.md) | [`examples/external_source`](examples/external_source/example_test.go) |

---

## 为什么选 FastConf

- **强类型读路径。** `mgr.Get().Server.Addr` 由编译器检查，无字符串路径、无反射、无 `interface{}`。
- **无锁热读。** `Get()` 就是一次 `atomic.Pointer.Load()` —— O(1)，零分配，任意数量 goroutine 安全。
- **失败安全热更新。** 任一 pipeline 阶段报错都保留旧 `*State[T]`，坏配置永远不会到达业务代码。
- **Kustomize 风格叠加。** 支持 base / overlay、RFC 6902 patch 以及列表对象的 `mergeKeys` 策略合并。
- **按需扩展。** Provider、Transformer、Secret Resolver、Validator、Policy、Metrics、Tracing 全部可选。

---

## 安装

```bash
go get github.com/fastabc/fastconf@latest

# 可选子模块：
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/cue@latest
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/providers/s3@latest
```

命令行工具（需要 Go ≥ 1.22）：

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

### 兼容性

| 项目 | 支持范围 |
|---|---|
| Go 工具链 | 1.22, 1.23, 1.24, 1.25, 1.26（`go.mod` 不再固定 toolchain） |
| 操作系统 / 架构 | linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64（每个 tag 都会发布二进制） |
| 模块形态 | 一个根模块 + 独立子模块（`cue`、`policy/opa`、`validate/playground`、`observability/{otel,metrics/prometheus}`、`providers/s3`、`integrations/{cli/pflag,log/phuslu,log/zerolog}`） |
| 预发布约定 | 语义化版本 `vMAJOR.MINOR.PATCH`。当前 `v0.18` 是首个公开版本，rename / bucketed-Options 边界已锁定，详见 [migration-v0.18.md](docs/cookbook/migration-v0.18.md)。 |

### 版本策略

- Tag 形式 `vMAJOR.MINOR.PATCH`。根模块与所有子模块通过
  `tools/tag-release.sh vX.Y.Z` 一次性打上相同 tag。
- 主版本号 `0` 保留给 pre-1.0 阶段。v1.0 之前的 minor 版本之间仍可能出现
  不兼容变更，但每次都会附带 `docs/cookbook/` 下的迁移指南，让调用点的
  改动保持机械可执行。
- `internal/*` 下的包属于实现细节，不在 SemVer 契约内 —— 根包的
  re-export（type alias 或 wrapper）才是稳定的对外面。
- `pkg/*` 中的可复用原语保持单向依赖（详见 `CLAUDE.md` 的依赖白名单），
  由 `tools/check-deps.sh` 在 CI 中静态强制；调用方可放心引用单个
  `pkg/*` 子包而不会被引入隐藏的横向依赖。
- 子模块独立打 tag 时尾后缀使用模块路径名，例如 `cue/vX.Y.Z` —— 在
  README 主表中通常不必关心，因为同一发版统一推送同一版本号。
- 发版前会运行 `make test` + `tools/{check-layout,check-deps,
  check-doc-symbols,audit-phase-comments,check-cjk-comments,
  loc-budget,total-loc-budget}.sh` 共 7 个 guard 脚本，保证目录布局、
  依赖方向、对外符号、注释考古与体积红线全部满足约束。

---

## 核心模型

```text
sources / generators / providers
              │
              ▼
       assemble preflight
              │
              ▼
 merge → migration → transform → secret → typed-hooks
      → decode → field-meta → validate → policy
              │
      fail ───┴─── keep old State[T]
              │
           success
              ▼
 canonical hash → atomic swap → history → audit → subscribers
```

| 设计 | 含义 |
|---|---|
| 强类型读路径 | `mgr.Get().Server.Addr`，由编译器检查 |
| 单写者 reload | fsnotify、provider 事件、手动 `Reload` 都串行进入同一写路径 |
| 失败安全 | 任一阶段失败都保留旧 `State[T]`，坏配置不会发布给业务 |
| Kustomize 风格叠加 | base / overlay、RFC 6902 patch、`mergeKeys` 策略合并 |
| 可选扩展 | provider、transformer、secret resolver、policy、metrics、tracer 均可选 |

---

## Manager API

```go
// 构造（首次 reload 同步完成）
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error)

// 读路径 — 无锁，O(1)，零分配
func (m *Manager[T]) Get() *T

// 触发一次 reload；ctx 贯穿整个 pipeline
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error

// 预演（dry-run）— 不更新 atomic pointer
func (m *Manager[T]) Plan() *PlanBuilder[T]

// 当前快照（State[T] + Sources + Origins）
func (m *Manager[T]) Snapshot() *State[T]

// 异步错误流 — 缓冲 16，满则丢弃，Close() 时关闭
func (m *Manager[T]) Errors() <-chan ReloadError

func (m *Manager[T]) Watcher() *Watcher[T]  // .Pause() / .Resume()
func (m *Manager[T]) Replay()  *Replay[T]   // .List() / .Rollback(*State[T])
func (m *Manager[T]) Close() error
```

包级泛型函数：

```go
// 字段级订阅；仅在提取出的值实际发生变化时触发。
// 传入 WithEqual(eq) 可替换默认的 reflect.DeepEqual 比较器。
func Subscribe[T, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M), opts ...SubscribeOption[M]) (cancel func())
func WithEqual[M any](equal func(old, new *M) bool) SubscribeOption[M]

// 类型安全的 feature flag 求值
func Eval[T, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V
```

`State[T]` 包含 `Value *T`、`Hash [32]byte`、`Generation uint64`、
`Sources []SourceRef`，以及来源追溯辅助方法（`Explain`、`Diff`、`Redacted`）。

---

## Option 参考

所有 `WithXxx` 选项返回 `Option`，可按任意顺序传给 `New[T]`。
完整参考请见 [docs/readme/zh/02-core-model.md](docs/readme/zh/02-core-model.md)。

### 常用 Option

| Option | 用途 | 默认值 |
|---|---|---|
| `WithDir(dir)` | 配置根目录 | `"conf.d"` |
| `WithFS(fs.FS)` | 替代 `fs.FS`（用于测试） | — |
| `WithWatch(WatchOptions{...})` | 启用 fsnotify；桶式字段 `Enabled` / `Paths` / `Coalesce` / `CoalesceProfile` | `Enabled:false` |
| `WithProfile(ProfileOptions{...})` | profile 选择桶：`Single` / `Multi` / `Expr` / `EnvVar` / `Default` | — |
| `WithCoalesce(CoalesceOptions{...})` | 仅微调 watcher 的 `Quiet` / `MaxLag` / `SwapHint`，不改动 `WithWatch` | — |
| `WithProvider(p)` | 注册结构化 provider | — |
| `WithSource(src, parser)` | 字节流 source + parser | — |
| `WithMigrations(fn)` | schema 迁移回调 | — |
| `WithTransformers(t...)` | 合并后变换链 | — |
| `WithSecretResolver(r)` | decode 前解密 leaf | — |
| `WithValidator[T](fn)` | decode 后类型校验 | — |
| `WithPolicy[T](p)` | 校验后策略求值 | — |
| `WithHistory(n)` | 保留最近 `n` 个成功状态 | — |
| `WithProvenance(level)` | `Off` / `TopLevel` / `Full` | `Off` |
| `WithMetrics(sink)` | 指标 sink | — |
| `WithAuditSink(sink)` | 每次成功 reload 审计回调 | — |
| `WithTracer(tracer)` | OTel 兼容 tracer | — |
| `WithLogger(*slog.Logger)` | 注入 logger | `io.Discard` |
| `WithStructDefaults[T]()` | 通过 `fastconf:"default=…"` tag 填充零值 | — |

---

## Reload Pipeline

### 阶段序列

```
reloadCh.recv(req)
  ├─ stageMerge:      discovery.Scan(dir) → 解码文件 → merger.Merge(layers)
  │                   应用 _meta.yaml（appendSlices / profileEnv / match）
  │                   应用 _patch.json（RFC 6902）
  ├─ stageAssemble:   各 provider: Load(ctx) → 按 Priority 合并
  ├─ stageMigrate:    opts.migrationRun(merged)
  ├─ stageTransform:  各 transformer: t.Transform(merged)
  ├─ stageDecode:     json.Marshal(merged) → json.Unmarshal(→ *T)
  ├─ stageFieldMeta:  range / enum / required 检查
  ├─ stageValidate:   各 validator: v(*T)
  ├─ stagePolicy:     各 policy: p.Evaluate(ctx, *T, reason, tenant)
  └─ commit:
       canonical SHA-256 去重
       atomic.Pointer.Store(newState) → history → audit → subscribers
```

任一阶段报错时：`atomic.Pointer` **不**更新，`Generation` **不**递增，
错误通过 `Errors()` 异步广播，`AuditSink` **不**触发。

---

## Profile 与 Overlay

```text
conf.d/
  base/                     # 所有 profile 均应用
    00-defaults.yaml
  overlays/
    prod/
      50-prod.yaml
      _meta.yaml            # profile 匹配表达式
      _patch.json           # RFC 6902 patch
```

### `_meta.yaml`

```yaml
schemaVersion: "1"
profileEnv: "APP_PROFILE"
defaultProfile: "dev"
appendSlices: true
match: "prod | staging"     # 支持 &、|、!、()
```

### RFC 6902 JSON Patch

```json
[
  { "op": "replace", "path": "/server/addr",      "value": ":8443" },
  { "op": "add",     "path": "/feature/darkMode", "value": true    },
  { "op": "remove",  "path": "/legacy/key"                         }
]
```

多 profile 模式：`WithProfile(ProfileOptions{Multi: []string{"prod", "eu-west", "canary"}})`
—— 每个 overlay 的 `_meta.yaml.match` 决定是否应用。

---

## Provider 系统

### 内置结构化 Provider（`pkg/provider`）

| Provider | 构造函数 | 说明 |
|---|---|---|
| Env | `provider.NewEnv("APP_")` | `APP_FOO_BAR` → `foo.bar`；支持 `.WithReplacer`、`.At`、`.WithCoerce` |
| CLI | `provider.NewCLI(map)` | 仅传入用户显式设置的 flag，文件/env 保持权威 |
| DotEnv | `provider.NewDotEnv("APP_", paths...)` | `.env` 兜底；进程环境变量优先 |
| Labels | `provider.NewDottedLabels(labels, opts)` / `NewRoutingLabels(labels, opts)` | 配置标签与路由 DSL 标签 |
| K8s Downward | `k8s.NewDefault()` | 读取 `/etc/podinfo/{labels,annotations}` |

根模块 KV Provider（可通过 build tag 裁剪）：

```go
vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
// 裁剪：-tags no_provider_vault,no_provider_consul,no_provider_http
```

随根模块发布的事件 Provider：NATS（`providers/nats`）和 Redis Streams
（`providers/redisstream`）。独立 Provider 子模块（按需 `go get`）：
S3（`providers/s3`）。

### 优先级常量

合并顺序按 `Priority()` 升序——值越大越后覆盖：

| 常量 | 值 | 用途 |
|---|---:|---|
| `PriorityDotEnv` | 5 | `.env` 兜底 |
| `PriorityStatic` | 10 | 静态 / 文件层 |
| `PriorityKV` | 30 | Vault / Consul / HTTP / S3 |
| `PriorityK8s` | 40 | Kubernetes ConfigMap / Secret |
| `PriorityEnv` | 50 | 进程环境变量 |
| `PriorityCLI` | 60 | 命令行参数（最高） |

使用 `WithProviderOrdered(p1, p2, p3)` 可按调用顺序自动分配优先级。

### `contracts.Provider` 接口

```go
type Provider interface {
    Name()     string
    Priority() int
    Load(ctx context.Context) (map[string]any, error)
    Watch(ctx context.Context) (<-chan Event, error)
}
```

---

## Transformer 与迁移

### 内置 Transformer（`pkg/transform`）

```go
fastconf.WithTransformers(
    transform.Defaults(map[string]any{"server": map[string]any{"timeout": "30s"}}),
    transform.SetIfAbsent("server.timeout", "30s"),
    transform.EnvSubst(),                           // ${VAR} / ${VAR:-default}
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{"db.url": "database.dsn"}),
)
```

### Struct Tag

```go
type AppConfig struct {
    Server struct {
        Addr    string        `json:"addr"    fastconf:"default=:8080"`
        Timeout time.Duration `json:"timeout" fastconf:"default=30s"`
    } `json:"server"`
    Database struct {
        DSN string `json:"dsn" fastconf:"secret"` // 在日志/快照中脱敏
    } `json:"database"`
}
```

### 迁移

```go
fastconf.WithMigrations(func(root map[string]any) error {
    if v, ok := root["db_url"]; ok {
        db, _ := root["database"].(map[string]any)
        if db == nil { db = map[string]any{}; root["database"] = db }
        if _, has := db["dsn"]; !has { db["dsn"] = v }
        delete(root, "db_url")
    }
    return nil
})
```

多步 schema 迁移请使用 `pkg/migration.NewChain`。

---

## Watch、Subscribe 与 Plan

### 字段级订阅

`Subscribe` 仅在提取出的值真正发生变化时触发回调（默认按 DeepEqual 对解
引用后的值比较）。通过 `WithEqual` 传入自定义比较器，可忽略噪声字段、对
大结构体走哈希比较，或恢复 v0.18 的"每次 reload 都触发"语义。

```go
cancel := fastconf.Subscribe(mgr,
    func(app *AppConfig) *DatabaseConfig { return &app.Database },
    func(old, neu *DatabaseConfig) {
        reconnect(neu.DSN) // 框架保证：DB 配置确实变了
    },
)
defer cancel()

// 自定义比较（忽略 Pool 字段）。
fastconf.Subscribe(mgr,
    func(app *AppConfig) *DatabaseConfig { return &app.Database },
    func(_, neu *DatabaseConfig) { warmCache(neu) },
    fastconf.WithEqual(func(a, b *DatabaseConfig) bool { return a.DSN == b.DSN }),
)

// 每次 reload 都触发（v0.18 行为的兼容写法）。
fastconf.Subscribe(mgr,
    func(app *AppConfig) *AppConfig { return app },
    func(_, neu *AppConfig) { auditEveryReload(neu) },
    fastconf.WithEqual(func(_, _ *AppConfig) bool { return false }),
)
```

### 手动 Reload 与一次性覆盖

```go
err := mgr.Reload(ctx,
    fastconf.WithReloadReason("admin-cli"),
    fastconf.WithSourceOverride(map[string]any{
        "server": map[string]any{"addr": ":9999"},
    }),
)
```

### Plan（预演）

```go
result, err := mgr.Plan().WithHostname("ci-runner-7").Run(ctx)
// result.Validators — 校验错误
// result.Policies   — 策略违规（预演中 SeverityError 降级为 warning）
```

### 暂停 / 恢复

```go
mgr.Watcher().Pause()
applyBatchUpdate()
mgr.Watcher().Resume()
```

---

## 来源追溯、历史与回滚

### 来源追溯

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvenance(fastconf.ProvenanceFull),
)

origins := mgr.Snapshot().Explain("server.addr")
// 每条 Origin：Source.Name、Source.Priority、Value
```

| 级别 | 开销 | 可追溯范围 |
|---|---|---|
| `ProvenanceOff` | 零 | 无 |
| `ProvenanceTopLevel` | O(顶级 key 数) | 每个顶级字段由哪一层设置 |
| `ProvenanceFull` | O(叶子节点数) | 每个叶子的完整覆盖链 |

### 历史与回滚

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithHistory(10),
)
history := mgr.Replay().List()     // []*State[T]，从旧到新
_ = mgr.Replay().Rollback(history[len(history)-2])
```

### 错误流

```go
go func() {
    for re := range mgr.Errors() {
        slog.Error("reload failed", "reason", re.Reason, "err", re.Err)
    }
}()
```

---

## 可观测性

```go
// 每次成功 reload 输出 JSON 审计行
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithAuditSink(fastconf.NewJSONAuditSink(os.Stderr)),
    fastconf.WithDiffReporter(fastconf.DiffReporterFunc(
        func(ctx context.Context, ev fastconf.DiffEvent) error {
            return slack.Post(ctx, ev.Diff) // 异步，不阻塞 reload
        },
    )),
)
```

Prometheus 指标与 OpenTelemetry 追踪在独立子模块中：

```go
import prommetrics "github.com/fastabc/fastconf/observability/metrics/prometheus"
import fastconfotel "github.com/fastabc/fastconf/observability/otel"

fastconf.WithMetrics(prommetrics.New())
fastconf.WithTracer(fastconfotel.NewTracer(otel.GetTracerProvider()))
```

策略违规在 `SeverityError` 时中止 reload；`SeverityWarning` 仅记录日志后继续。
CUE 与 OPA 实现分别在 `cue/policy` 和 `policy/opa`。

---

## 多租户与预设

```go
// 多租户：每个租户是完全隔离的 Manager[T]
tm := fastconf.NewTenantManager[AppConfig]()
mgrA, _ := tm.Add(ctx, "tenant-a", fastconf.WithDir("/etc/config/tenant-a"))
app, err := tm.Get("tenant-a")  // 不存在返回 fastconf.ErrUnknownTenant
tm.Close()
```

```go
// 预设
fastconf.PresetK8s(fastconf.K8sOpts{Dir: "/etc/config", Watch: true})
fastconf.PresetSidecar(fastconf.SidecarOpts{Dir: "/etc/fastconfd", HistoryN: 16})
fastconf.PresetTesting(fastconf.TestingOpts{FS: memFS, Profile: "testing"})
```

---

## 子模块生态

### 随根模块一起发布

| 包 | 路径 |
|---|---|
| contracts | `contracts` — 公开接口 |
| 可复用原语 | `pkg/{decoder,discovery,feature,flog,generator,merger,migration,provider,transform,validate}` |
| http / vault / consul | `providers/{http,vault,consul}` — build tag：`no_provider_{http,vault,consul}` |
| nats / redis-streams | `providers/{nats,redisstream}` — 调用方注入传输客户端 |
| policy | `policy` — `Func` 适配器 |
| sidecar 服务 | `cmd/fastconfd` |
| CLI 工具 | `cmd/{fastconfctl,fastconfgen}` |
| integrations | `integrations/{bus,openfeature,render}` |

### 独立子模块（按需 `go get`）

| 子模块 | 路径 | 主要依赖 |
|---|---|---|
| validate/playground | `validate/playground` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | prometheus/client_golang |
| otel | `observability/otel` | OpenTelemetry SDK |
| cue（校验 + 策略） | `cue` | cuelang.org/go |
| opa-policy | `policy/opa` | open-policy-agent/opa |
| cli/pflag | `integrations/cli/pflag` | spf13/pflag |
| s3 provider | `providers/s3` | AWS SDK v2 |

一次性打所有子模块 tag：`./tools/tag-release.sh vX.Y.Z [--push]`

---

## CLI 工具

### `fastconfd` — sidecar 服务

```bash
fastconfd --dir=/etc/config --profile=prod --addr=:8081
```

| 端点 | 方法 | 说明 |
|---|---|---|
| `/healthz` | GET  | `{"status":"ok","generation":N}` |
| `/version` | GET  | 版本、generation、hash、加载时间、原因 |
| `/config`  | GET  | 当前配置 JSON；传 `?redact=true` 才脱敏 |
| `/dump`    | GET  | 确定性 YAML（`?format=json` 输出 JSON） |
| `/reload`  | POST | 触发手动 reload |
| `/events`  | GET  | 每次成功 reload 的 SSE 事件流 |

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

## 性能

最新 benchmark：**Apple M2 / darwin-arm64 / Go 1.26.2**。

| Benchmark | 中位数 |
|---|---:|
| `BenchmarkGet` | 0.52 ns/op |
| `BenchmarkReloadNoop` | 15.1 µs/op |
| `BenchmarkReloadCommitSmall` | 16.5 µs/op |
| `BenchmarkReloadManySubscribers/50` | 17.5 µs/op |

完整基准：[`docs/design/perf.md`](docs/design/perf.md)。

---

## 本地开发

```bash
go mod tidy
make build
make test        # go test -race -count=1 ./...
make test-all    # 包含子模块
make lint        # 需要 golangci-lint

go test ./... -run '^Example' -v
go test -bench=BenchmarkGet -benchmem ./...
```

---

## 文档

| 文档 | 用途 |
|---|---|
| [docs/readme/zh/](docs/readme/zh/) | 深度章节：核心模型、pipeline、扩展机制、生产运维 |
| [docs/cookbook/README.md](docs/cookbook/README.md) | 按使用旅程整理的实战配方 |
| [docs/design/spec.md](docs/design/spec.md) | 运行时模型、并发、模块边界 |
| [docs/cookbook/migration-v0.18.md](docs/cookbook/migration-v0.18.md) | v0.18 重命名 / bucketed-Options 迁移表 |
| [docs/cookbook/migration-v0.19.md](docs/cookbook/migration-v0.19.md) | v0.19 `Subscribe` diff-aware 迁移说明 |
| [GitHub Releases](https://github.com/fastabc/fastconf/releases) | 版本发布说明与预编译 CLI 二进制 |
| [pkg.go.dev](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc 与可运行示例 |

常用 recipe：[k8s](docs/cookbook/k8s.md) · [vault](docs/cookbook/vault.md) ·
[consul](docs/cookbook/consul.md) · [secrets](docs/cookbook/secrets.md) ·
[features](docs/cookbook/features.md) · [policy](docs/cookbook/policy.md) ·
[otel](docs/cookbook/otel.md) · [tenant](docs/cookbook/tenant.md) ·
[sidecar](docs/cookbook/sidecar.md) · [plan](docs/cookbook/plan.md)

---

## License

MIT License, See [`LICENSE`](LICENSE).

Copyright (c) 2026 FastAbc
