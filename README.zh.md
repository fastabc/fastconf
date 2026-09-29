# FastConf — 强类型 · 无锁 · Kustomize 风格配置框架

> **语言**: [English](README.md) · 中文

`fastconf` 把 YAML / JSON / TOML、环境变量、命令行参数、远程 KV 与生成器
layer 叠加成一个强类型 Go 结构体，并在热更新时用单写者 reload loop 和
`atomic.Pointer` 安全发布不可变快照。业务读路径就是一次 `atomic.Pointer.Load()`，
并保持零分配。

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **状态**：v1.0.0 本文描述候选版 API；
> 从已发布 v0 升级请参阅 [v1 迁移指南](docs/cookbook/migration-v1.md)。

---

## 目录

1. [快速上手](#快速上手)
2. [为什么选 FastConf](#为什么选-fastconf)
3. [安装](#安装)
4. [核心模型](#核心模型)
5. [Manager API](#manager-api)
6. [Reload Pipeline](#reload-pipeline)
7. [Profile 与 Overlay](#profile-与-overlay)
8. [Provider 系统](#provider-系统)
9. [Transformer 与迁移](#transformer-与迁移)
10. [Watch、Subscribe 与 Plan](#watchsubscribe-与-plan)
11. [来源追溯、历史与回滚](#来源追溯历史与回滚)
12. [可观测性](#可观测性)
13. [多租户与常用组合](#多租户与常用组合)
14. [子模块生态](#子模块生态)
15. [CLI 工具](#cli-工具)
16. [性能](#性能)
17. [本地开发](#本地开发)
18. [文档](#文档)
19. [License](#license)

---

## 快速上手

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

使用环境变量覆盖运行：

```bash
APP_PROFILE=prod APP_DATABASE_POOL=20 go run .
```

`APP_DATABASE_POOL=20` 映射到 `database.pool`（单下划线分隔符，Viper/Spring Boot 风格）。
设置 `APP_PROFILE=prod` 后，FastConf 先合并 `base/*`，再合并 `overlays/prod/*`。

---

## 为什么选 FastConf

- **强类型读路径。** `mgr.Get().Server.Addr` 由编译器检查，无字符串路径、无反射、无 `interface{}`。
- **无锁热读。** `Get()` 就是一次 `atomic.Pointer.Load()` —— O(1)，零分配，任意数量 goroutine 安全。
- **失败安全热更新。** 任一 pipeline 阶段报错都保留旧 `*State[T]`，坏配置永远不会到达业务代码。
- **Kustomize 风格叠加。** 支持 base / overlay、RFC 6902 patch 以及列表对象的 `mergeKeys` 策略合并。
- **按需扩展。** Provider、Transformer、Secret Resolver、Validator、Policy、Metrics、Tracing 全部可选。

---

## 安装

根模块要求 Go 1.24+。子模块最低为 Go 1.24；依赖需要更高版本时，在各自的 go.mod 中声明。
**v1.0.0 已准备，尚未打 tag**。目前使用本地 checkout 构建：

```bash
go build ./...
go build ./cmd/fastconfctl ./cmd/fastconfd
```

发布后可安装：

```bash
go get github.com/fastabc/fastconf@v1.0.0
go get github.com/fastabc/fastconf/integrations/log/zerolog@v1.0.0  # 或 .../log/phuslu
go get github.com/fastabc/fastconf/observability/metrics/prometheus@v1.0.0
go get github.com/fastabc/fastconf/observability/otel@v1.0.0
```

根模块 tag 为 `vX.Y.Z`，独立模块为 `<module>/vX.Y.Z`，所有模块统一发布同一版本。
详见[发布流程](RELEASING.md)。

---

## 核心模型

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

| 设计 | 含义 |
|---|---|
| 强类型读路径 | `mgr.Get().Server.Addr`，由编译器检查 |
| 单写者 reload | fsnotify、provider 事件、手动 `Reload` 都串行进入同一写路径 |
| 失败安全 | 任一阶段失败都保留旧 `State[T]`，坏配置不会发布给业务 |
| Kustomize 风格叠加 | base / overlay、RFC 6902 patch、`mergeKeys` 策略合并 |
| 可选扩展 | provider、transformer、secret resolver、policy、metrics、tracer 均可选 |

---

## Manager API

`New[T]` 同步加载后启动监听；`Load[T]` 只加载一次。`Get()` 返回只读的 `*T`，
`Snapshot()` 提供诊断视图。`Reload(ctx)` 与 `Plan(ctx)` 共享单写者队列；
Plan 保留所有验证结果（包括成功项）及策略发现，不发布状态。
使用完毕调用 `Close()`；需要限制等待时间时使用 `Shutdown(ctx)`。
完整签名见 [API 文档](https://pkg.go.dev/github.com/fastabc/fastconf)。

---

## Reload Pipeline

### 阶段序列

```
reloadCh.recv(req)
  ├─ assemble:     scan files → generators → provider.Load(ctx) → sort layers
  ├─ merge:        deep / strategic merge + RFC 6902 patches
  ├─ transform:    registered map functions (including schema migrations)
  ├─ secret:       resolve recognized secret references
  ├─ typed-hooks:  rewrite duration and custom scalar values
  ├─ decode:       JSON (or YAML) → *T → tag defaults → Defaulter
  ├─ field-meta:   required / range / enum checks
  ├─ validate:     registered validators
  ├─ policy:       evaluate policies; errors reject the candidate
  └─ commit:       hash final *T → skip identical hash → atomic swap
                   retain previous snapshot → observers → subscribers
```

任一阶段报错时：`atomic.Pointer` **不**更新，`Generation` **不**递增，
错误通过 `Errors()` 异步广播，observer 收到 `ReloadFinished{Err}`，**没有** `Committed`。

---

## Profile 与 Overlay

先应用 `base/`，再应用匹配的 `overlays/<profile>/`。
`WithProfile(Profile{Names: []string{"prod", "eu-west"}})` 选择多个 profile；
各 overlay 的 `_meta.yaml.match` 支持 `&`、`|`、`!` 与括号。
根 `_meta.yaml` 配置默认项和合并行为，`.patch.json` 文件应用 RFC 6902 操作。

`WithAxes` 添加独立维度，较高 `Axis.Priority` 的整个目录优先；相同优先级按声明顺序，
目录内按文件名排序。最多支持 40 个 axis，每个 overlay 100 个文件，base 1000 个文件。
详见[运行时契约](docs/design/spec.md)。

---

## Provider 系统

### 内置结构化 Provider（`providers/*`）

| Provider | 构造函数 | 说明 |
|---|---|---|
| Env | `env.NewEnv("APP_")`（`providers/env`） | `APP_FOO_BAR` → `foo.bar`；支持 `.WithReplacer`、`.At`、`.WithCoerce` |
| CLI | `cliflag.NewCLI(map)`（`providers/cliflag`） | 仅传入用户显式设置的 flag，文件/env 保持权威 |
| DotEnv | `dotenv.NewDotEnv("APP_", paths...)`（`providers/dotenv`） | `.env` 兜底；进程环境变量优先 |
| Labels | `labels.New(labels, labels.Options{})`（`providers/labels`） | dotted 配置标签；`Options.Routing` 开启路由 DSL |
| K8s Downward | `k8s.NewDefault()` | 读取 `/etc/podinfo/{labels,annotations}` |

根模块 KV Provider（仅链接实际导入的包）：

```go
vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
```

NATS 与 Redis Streams 参考实现已移至 `examples/nats` 和 `examples/redisstream`，
可复制后接入真实客户端。HTTP 的 nil codec 按 Content-Type 解码，`WithInterval(0)` 关闭轮询。
独立 Provider 子模块（按需 `go get`）：
S3（`providers/s3`）。

Provider 优先级从低到高：DotEnv (5)、Static (10)、KV (30)、K8s (40)、Env (50)、CLI (60)。
同优先级按注册顺序。自定义实现 `contracts.Provider`（`Name`、`Load`、`Watch`），
可选的 `contracts.Describer` 提供优先级与文件监听路径。

---

## Transformer 与迁移

```go
fastconf.WithTransform(
    transform.EnvSubst(),
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{"db.url": "database.dsn"}),
)
fastconf.WithMergeKeys(map[string]string{"listeners": "name"})
```

Transform 在合并后运行。跨层按键合并列表使用 `WithMergeKeys` 或 `_meta.yaml`。
默认值使用 `fc:"default=…"` 或 `Defaulter.Defaults`；`fc:"secret"` 标记敏感字段。
`transform.New` 构造迁移链。参阅[迁移](docs/cookbook/migration-v1.md)与
[密钥](docs/cookbook/secrets.md)。

---

## Watch、Subscribe 与 Plan

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

Subscribe 比较提取的值，`WithEqual` 可自定义相等规则。Override 只作用于当次 reload。
Plan 返回候选状态、diff、验证结果和策略发现。`Pause()` 忽略文件和 Provider
变更事件；`Resume()` 恢复监听，不重放暂停期间忽略的事件。批量更新结束后，
显式调用 `Reload(ctx)` 加载最终状态。
参阅[订阅](docs/cookbook/observer.md)与[预览](docs/cookbook/plan.md)。

---

## 来源追溯、历史与回滚

使用 `WithProvenance(ProvenanceFull)` 后，`mgr.Snapshot().Explain("server.addr")`
返回叶子路径的来源链。`ProvenanceTopLevel` 仅记录顶层键，`ProvenanceOff` 关闭记录。
列表密钥使用 `items.0` 格式的路径。

`WithHistory(10)` 保留旧快照，`mgr.History().List()` 按时间由旧到新排列，
将保留的快照传给 `Rollback` 即可回滚。零关闭历史，负数报错。失败重载进入 `mgr.Errors()`。
快照的 `Map`、`Dump`、`Diff`、`Explain` 默认脱敏，明确需要明文时使用 `Unredacted()`。
详见[历史](docs/cookbook/introspect.md)。

---

## 可观测性

`WithObserver` 接收 reload、stage、provider 错误、事件丢弃和 commit 通知。
通过 `observe.Multi`、`observe.JSONLines`、`observe.Metrics`、`observe.Func` 组合行为。
慢回调使用 `observe.Async`，先关闭 manager，再关闭异步 observer 以排空队列。

Prometheus（`observability/metrics/prometheus`）和 OpenTelemetry（`observability/otel`）
分别保留独立模块。日志适配包 `integrations/log/phuslu`、`integrations/log/zerolog`
各自是独立模块，共享随根模块发布的零依赖 logging 核心，只引入各自的后端。参阅[观测](docs/cookbook/observability.md)与[日志](docs/cookbook/log.md)。

---

## 多租户与常用组合

```go
// 多租户：每个租户是带 id 标签、完全隔离的 Manager[T]；
// 用你自己的注册表管理它们（见 docs/cookbook/tenant.md）。
mgrA, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("/etc/config/tenant-a"),
    fastconf.WithTenant("tenant-a"),
)
```

---

## 子模块生态

### 随根模块一起发布

| 包 | 路径 |
|---|---|
| contracts | `contracts` — 公开接口 |
| 可复用原语 | `codec`、`confmap`、`transform`、`feature`、`providers/{env,cliflag,dotenv,labels,source}` |
| http / vault / consul | `providers/{http,vault,consul}` |
| policy | `policy` — `Func` 适配器 |
| sidecar 服务 | `cmd/fastconfd` |
| CLI 工具 | `cmd/fastconfctl`（根模块）、`cmd/fastconfgen`（独立 module） |
| integrations | `integrations/render` |

### 独立子模块（按需 `go get`）

| 子模块 | 路径 | 主要依赖 |
|---|---|---|
| validate/playground | `validate/playground` | go-playground/validator |
| Prometheus | `observability/metrics/prometheus` | Prometheus |
| OpenTelemetry | `observability/otel` | OpenTelemetry |
| phuslu adapter | `integrations/log/phuslu` | phuslu/log |
| zerolog adapter | `integrations/log/zerolog` | zerolog |
| generator | `cmd/fastconfgen` | yaml.v3 |
| cue（校验 + 策略） | `cue` | cuelang.org/go |
| opa-policy | `policy/opa` | open-policy-agent/opa |
| cli/pflag | `integrations/cli/pflag` | spf13/pflag |
| s3 provider | `providers/s3` | AWS SDK v2 |

统一为所有模块发布同一版本：`./tools/tag-release.sh vX.Y.Z [--push]`

---

## CLI 工具

### `fastconfd` — sidecar 服务

```bash
fastconfd --dir=/etc/config --profile=prod --addr=127.0.0.1:8081 \
  --read-token="$FASTCONFD_READ_TOKEN" --reload-token="$FASTCONFD_RELOAD_TOKEN"
```

| 端点 | 方法 | 说明 |
|---|---|---|
| `/healthz` | GET  | 首次加载成功后返回纯文本 `ok` |
| `/version` | GET  | 版本、generation、hash、加载时间、原因 |
| `/config`  | GET  | 当前配置 JSON，默认脱敏；明文需 `?unredacted=true` + `X-Unredacted-Token` |
| `/dump`    | GET  | 确定性 YAML（`?format=json` 输出 JSON） |
| `/reload`  | POST | 触发手动 reload |
| `/events`  | GET  | 新快照提交时发送的 SSE 事件流 |

`FASTCONFD_READ_TOKEN` 和 `FASTCONFD_RELOAD_TOKEN` 必须非空。
`/config`、`/dump`、`/events` 需要 `X-Config-Token`；`/reload` 需要
`X-Reload-Token`。详见 [sidecar 配方](docs/cookbook/sidecar.md)。

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

性能约束和可复现的测量命令记录在 [Performance Notes](docs/design/perf.md)。
使用 `tools/profile-reload.sh` 在目标机器上复测；`tools/bench-guard.sh` 检查热读延迟和零分配约束。

---

## 本地开发

```bash
go mod tidy
make build
make test        # go test -race -count=1 ./...
make test-workspace # 所有模块使用当前 checkout
make test-candidate # 发布前验证独立依赖解析
make lint        # 需要 golangci-lint
make check       # 发布工具回归测试

go test ./... -run '^Example' -v
go test -bench=BenchmarkGet -benchmem ./...
```

---

## 文档

| 文档 | 用途 |
|---|---|
| [docs/cookbook/README.md](docs/cookbook/README.md) | 按使用旅程整理的实战配方 |
| [docs/design/spec.md](docs/design/spec.md) | 运行时模型、并发、模块边界 |
| [docs/cookbook/migration-v1.md](docs/cookbook/migration-v1.md) | v0 → v1 迁移指南 |
| [docs/cookbook/migration-v0.md](docs/cookbook/migration-v0.md) | v0 版本之间的迁移归档 |
| [GitHub Releases](https://github.com/fastabc/fastconf/releases) | 版本发布说明与预编译 CLI 二进制 |
| [pkg.go.dev](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc 与可运行示例 |

常用 recipe：[k8s](docs/cookbook/k8s.md) · [vault](docs/cookbook/vault.md) ·
[consul](docs/cookbook/consul.md) · [secrets](docs/cookbook/secrets.md) ·
[features](docs/cookbook/features.md) · [policy](docs/cookbook/policy.md) ·
[otel](docs/cookbook/observability.md#opentelemetry) · [tenant](docs/cookbook/tenant.md) ·
[sidecar](docs/cookbook/sidecar.md) · [plan](docs/cookbook/plan.md)

---

## License

MIT License, See [`LICENSE`](LICENSE).

Copyright (c) 2026 FastAbc
