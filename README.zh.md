# FastConf

[English](README.md) · [中文](README.zh.md)

FastConf 将文件、环境变量与云端配置统一为强类型 Go 配置。既可为 CLI 一次加载，
也可为常驻服务提供经过校验的热更新、原子快照，以及无锁、零分配的 `Get()`。
重载失败时，应用继续使用上一次有效配置。

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

本 README 介绍当前源码中计划用于 v1.6.0 的功能，见 [CHANGELOG.md](CHANGELOG.md)
的 `Unreleased`；标注 v1.6.0 的新增能力对应本次源码。
以下固定版本的安装示例使用 v1.5.0，版本行为请以所安装 tag 的文档为准。
从 v0.20.0 升级见[迁移指南](docs/cookbook/migration-v1.md)；从 v1.4.0 升级见
[v1.5.0 升级说明](docs/cookbook/migration-v1.md#upgrading-from-v140-to-v150)。

## 为什么选择 FastConf

- **一个结构体，统一多种来源。** 组合 YAML、JSON、TOML、INI、环境变量、参数和远程
  Provider，通过 Profile、region/host 分层、补丁与按键合并列表表达部署差异。
- **配置更容易写。** v1.6.0 提供易读的 `default` 标签并保留显式零值，将 KV 字符串
  转为强类型列表，用插值连接环境变量与配置字段。
- **运行时变更可预览、可恢复。** `Plan` 预演、发布前校验、按子树订阅及历史回滚，
  让数据库、日志等资源按实际变化更新；重载失败继续使用旧配置。
- **云端刷新减少应用接线。** v1.6.0 的 `WithNotifier` 托管通知消费者，
  `WithResync` 周期对账，为漏通知提供恢复路径。
- **诊断中自动隐藏密钥。** v1.6.0 支持串联 Vault、age 和应用自定义 resolver，
  解析结果自动在 `Map`、`Dump`、`Diff`、`Explain` 中脱敏。
- **配置类型也是文档来源。** v1.6.0 从同一结构体、默认值和字段描述生成 JSON Schema、
  带注释的 YAML 与 env 示例，减少配置与示例的重复维护。
- **从开发到运维都有入口。** 来源追溯、变更审计、指标、追踪、CLI 和 Sidecar 配合使用；
  云 SDK 与遥测后端位于可选模块，按需引入。

## 安装

根模块要求 Go 1.25+。可选模块的最低 Go 版本以各自的 `go.mod` 为准，按需安装。

```sh
go get github.com/fastabc/fastconf@v1.5.0
```

## 快速上手

按配置的生命周期选择入口：

| 需求 | API | 指南 |
|---|---|---|
| 启动时读取一个必需文件 | `LoadFile[T](path)` → `*T` | [单文件加载](docs/getting-started/load.md)；v1.2.0 新增 |
| 一次加载多层配置，使用选项和诊断 | `Load[T](ctx, opts...)` → `*State[T]` | [分层示例](examples/basic/README.md) |
| 运行时重载配置 | `New[T](ctx, opts...)` → `*Manager[T]` | [动态管理](docs/getting-started/dynamic.md) |

创建 `conf.d/base/00-app.yaml`：

```yaml
server:
  port: 8080
```

在应用模块中将以下完整程序保存为 `main.go`：

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
MYAPP_SERVER_PORT=0 go run .        # Validation error
```

所有输入格式默认使用 `json` 结构体标签。`Load` 不启动后台任务，返回值及其可达的
map、slice、pointer 均须保持只读。运行时更新使用 `New`，以 `WithWatch` 开启文件
监听，并在退出时关闭 Manager，见完整的[动态示例](examples/dynamic/README.md)。

在仓库根目录运行内置示例：

```sh
go run ./examples/basic
go run ./examples/reload
```

### 只读取一个必需文件

`LoadFile` 在 v1.2.0 新增，使用上面的 `Config` 类型和 YAML：

```go
cfg, err := fastconf.LoadFile[Config]("conf.d/base/00-app.yaml")
if err != nil {
    return err // Inside a function returning error.
}
log.Printf("port=%d", cfg.Server.Port)
```

只读取指定文件，不应用环境变量或 Profile 发现。未知键和文件缺失直接报错。
返回值由调用方拥有，无需关闭 Manager。见[单文件指南](docs/getting-started/load.md)。

## 功能分类

| 能力 | 可实现的场景 | 指南 |
|---|---|---|
| 分层与强类型加载 | 一个配置类型接入文件、env/dotenv、参数、Labels、HTTP、Vault、Consul 与 AWS/Azure/GCP 来源 | [来源配方](docs/cookbook/README.md#sources-and-layering) |
| 部署差异覆盖 | base/overlay Profile、region/host 维度、RFC 6902 补丁、按键列表合并与生成层 | [分层](docs/cookbook/sources/layers.md)、[按键列表](docs/cookbook/processing/strategic-merge.md)、[生成器](docs/cookbook/processing/generators.md) |
| 易读默认值与转换（v1.6.0） | `default:"5s"`、`default:"a,b"`、命名基础类型、逗号/JSON/下标 map 标量列表；Azure JSON 值可按需开启 | [字段元数据](docs/cookbook/processing/field-meta.md)、[类型钩子](docs/cookbook/processing/typed-hooks.md) |
| 环境变量与字段插值（v1.6.0） | `${VAR:default}`、必需值检查与 `${server.base_url}` 引用，包含环检测 | [插值](docs/cookbook/processing/interpolation.md) |
| 发布前校验 | 字段约束、Go validator 与策略，可选 OPA/CUE 适配器 | [校验](docs/cookbook/processing/field-meta.md)、[策略](docs/cookbook/processing/policy.md) |
| 动态更新与恢复 | 文件/Provider 监听、子树订阅、单次覆盖、Plan 预演、历史与回滚 | [订阅](docs/cookbook/runtime/subscribe.md)、[Plan](docs/cookbook/runtime/plan.md)、[历史](docs/cookbook/runtime/introspect.md) |
| 托管云通知（v1.6.0） | SNS/SQS、Service Bus、Pub/Sub 消费者由 Manager 托管重启、关闭和周期对账 | [云通知](docs/cookbook/sources/cloud-notifications.md) |
| 密钥解析与脱敏（v1.6.0） | 有序 resolver、Vault KV v2 字段引用、可选 age 解密，解析结果在诊断中自动隐藏 | [密钥](docs/cookbook/processing/secrets.md) |
| Schema 与示例生成（v1.6.0） | 从结构体标签生成 Draft 2020-12 JSON Schema、带注释的 YAML 与 env 示例 | [生成指南](docs/cookbook/processing/schema.md)、[可运行示例](examples/schema/README.md) |
| 运维与部署 | Explain 来源、脱敏 diff、Observer、指标、追踪、日志、CLI、Kubernetes Sidecar 与每租户独立 Manager | [可观测性](docs/cookbook/observability/observability.md)、[Sidecar](docs/cookbook/deployment/sidecar.md)、[多租户](docs/cookbook/deployment/tenant.md) |
| 特性规则 | 配置驱动的目标匹配与确定性百分比灰度 | [特性开关](docs/cookbook/features/features.md) |

覆盖顺序为：文件 → 生成器 → Provider → 单次 override。Provider 优先级只在
Provider 之间排序。FastConf 借鉴 Kustomize 的 base/overlay 与补丁思想，
不读取 `kustomization.yaml`，也不实现完整的 Kubernetes strategic merge 协议。

生命周期、优先级、解码和所有权以[运行时契约](docs/design/spec.md)为准。
[扩展指南](docs/getting-started/extensions.md)列出 S3、pflag、校验、日志与遥测等
可选模块。NATS 和 Redis Streams 是需要应用适配的参考实现，不属于稳定 Provider。

## Benchmarks

以下为 **FastConf v1.6.0** 的 [2026-11-08 比较报告](benchmarks/compare/REPORT.zh.md)部分中位数：
Apple M5 Pro、Darwin/arm64、Go 1.26.10、GOMAXPROCS=4、GOGC=100；每组运行 6 次，每次 500ms。
FastConf 使用本地源码提交 `d0364e0`；完整报告记录源码校验、固定依赖版本与适用范围。

| 场景 | FastConf v1.6.0 | Viper 1.21.0 | Koanf 2.3.0 | Cleanenv 1.5.0 |
|---|---:|---:|---:|---:|
| 4 KiB YAML 启动加载（ms/op） | 0.1186 | 0.1012 | 0.1291 | 0.0691 |
| 64 KiB 有效重载（ms/op） | 1.6452 | 1.5454 | 1.9950 | — |
| 已发布结构体热读（ns/op） | 0.3900 | 0.4181 | 0.4363 | — |

三个热读路径均为 0 B/op、0 allocs/op。Viper 和 Koanf 使用比较工程的适配器，
完成解码、校验、去重和原子发布；热读直接读取结构体，不执行字符串键查询。
亚纳秒结果表示紧循环吞吐量，不代表请求延迟。结果取决于工作负载和机器，
没有测量真实云服务延迟。

复现步骤与测量边界见[比较工程](benchmarks/compare/README.md)，请在空闲机器上运行。
Get 零分配检查、重载基准与 profiling 命令见[性能契约](docs/design/perf.md)。

## 加载与重载流程

`Load` 返回快照且不启动后台任务。`New` 同步完成首次加载，再启动单写者和配置的
监听器，提供 `Get`、`Snapshot`、`Reload`、`Plan`、`Errors` 和历史记录。
构造时的 context 只约束初始化，后台任务需由拥有者关闭 Manager 来结束。

以下代码为集成片段，类型和变量由应用提供。

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

候选配置被拒绝时，当前快照和 generation 不变。类型值的哈希与解析得到的敏感路径均相同则跳过发布。
仅敏感路径变化时仍提交新快照，更新诊断、generation 和历史，但不调用业务值订阅回调。
`Errors()` 有界，满时淘汰最旧的待消费错误，应持续读取以处理运行期失败。
`WithUnknownFields` 控制未知结构体键；默认 `UnknownWarn` 需要 `WithLogger`
才能看到警告。`WithStrictMerge` 则控制原始合并类型冲突和文件发现行为。

### 文件、Profile 与合并规则

依次加载 `base/*`、匹配的 `overlays/*`，以及 `WithAxes` 指定的 region/host 等
维度目录。文件和匹配的 Profile 目录按字典序合并；`Profile.Names` 的顺序不决定
覆盖优先级。
非空的 axis 值（包括主机名兜底）必须是单段目录名；`.` / `..` 和路径分隔符
会被拒绝，并记录 axis 警告。

```go
fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}})
fastconf.WithAxes(fastconf.Axis{Dir: "regions", Env: "DEPLOY_REGION"})
fastconf.WithMergeKeys(map[string]string{"listeners": "name"})
```

map 递归合并，标量和列表默认替换。`mergeKeys` 使用非空字符串键对齐列表对象。
`.patch.json` / `.patch.yaml` 文件按层顺序应用 RFC 6902 操作。根 `_meta.yaml`
的默认项和合并选项位于 `spec` 下；overlay 的 `_meta.yaml` 使用顶层 `match`
表达式。见[文件、Profile 与补丁](docs/cookbook/sources/layers.md)。

### Provider 与生成器

| Provider / 入口 | Watch changes（变更监听） | Notifier（通知通道） | Features（能力） |
|---|---|---|---|
| `source.NewFile` | 共享文件监听 | — | 解码单文档；文件缺失贡献空层 |
| `source.NewBytes` | — | — | 复制内存文档，显式指定 codec |
| `source.NewFS` | — | — | 从嵌入或虚拟 `fs.FS` 读取必需文档；v1.2.0 新增 |
| `env.New` | — | — | 进程环境变量；下划线映射路径分隔符 |
| `dotenv.New` | 共享文件监听 | — | Dotenv 兜底，进程环境变量优先 |
| `cliflag.New` | — | — | 显式设置的参数；可选 pflag 适配器 |
| `labels.New` | — | — | 展开 dotted 配置标签，支持映射选项 |
| `k8s.NewDefault` | 共享文件监听 | — | 保留命名空间下的原始 Downward API 元数据 |
| `providers/http` | 原生轮询 | — | 远程文档、ETag / 内容哈希 |
| `providers/vault` | 原生元数据轮询 | — | KV v2，可选认证维护；v1.6.0 支持字段 resolver |
| `providers/consul` | 原生 blocking query | — | KV 树或文档 |
| [`appconfig`](providers/appconfig) | 通知 + 定期重载 | SNS → SQS | 已部署文档、会话 token、服务端轮询间隔 |
| [`s3`](providers/s3) | 通知 + 定期重载 | SNS → SQS | ETag 条件读取，可固定 VersionID |
| [`parameterstore`](providers/parameterstore) | 通知 + 定期重载 | EventBridge → SNS → SQS | 递归分页树、字符串值、可选 SecureString 解密 |
| [`azappconfig`](providers/azappconfig) | 通知 + 定期重载 | Event Grid → Service Bus | 单 prefix/label、冒号分层；v1.6.0 的 `WithJSONValues()` 解码 JSON 设置 |
| [`azblob`](providers/azblob) | 通知 + 定期重载 | Event Grid → Service Bus | Blob 文档、ETag 条件读取 |
| [`secretmanager`](providers/secretmanager) | 通知 + 定期重载 | Pub/Sub | 版本或 alias 文档、CRC32C 校验 |
| [`gcs`](providers/gcs) | 通知 + 定期重载 | Pub/Sub | 当前对象文档、generation 缓存，单次读取与 generation 一致 |

“—” 表示没有自动变更检测。共享文件监听需启用 `WithWatch`；原生 Provider 监听
由 `New` 独立启动。云 Provider 在 `Reload` 调用 `Load` 时刷新；v1.6.0 可用
`WithNotifier` 注册消费者、`WithResync` 设置周期对账，由 Manager 托管这些任务。
AppConfig 还需遵守服务端允许的下一次轮询时间。

| Notifier 模块 | 确认与失败处理 |
|---|---|
| [`integrations/notify/sns`](integrations/notify/sns) | 通过 SQS 接收 SNS 包装或原始消息；刷新成功后删除，失败时保留以便重新投递 |
| [`integrations/notify/azservicebus`](integrations/notify/azservicebus) | 通过 Service Bus PeekLock 接收 Event Grid schema；刷新成功后 Complete，失败时 Abandon |
| [`integrations/notify/pubsub`](integrations/notify/pubsub) | 接收 GCS / Secret Manager 事件；刷新成功后 Ack，失败时 Nack |

三个模块均在有界 context 内同步执行刷新回调。v1.6.0 由 Manager 对提前退出的消费者
执行带抖动的退避重启，退出时停止任务，并通过周期重载恢复漏通知：

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

`remote`、`consumer` 是应用创建的 Provider 和消费者，时间间隔需导入 `time`。
`Load` 不启动后台任务，会忽略这些选项。SDK 客户端仍由应用持有，每个需要收到所有
变更的独立副本应配置单独的 queue/subscription。
接线、重试、死信与定期校准见[云通知配方](docs/cookbook/sources/cloud-notifications.md)。
`s3events` 在 v1.3.0 中弃用，请迁移至
[`integrations/notify/sns`](integrations/notify/sns)。原 Watch 是 EventBridge → SQS 的尽力通知，在 Manager 重载完成前确认
消息；需要“刷新成功后确认”的场景应使用新增消费者。

加载方式与限制见[嵌入文件与云端来源](docs/cookbook/sources/cloud.md)。云 SDK 位于独立
模块；除 S3 加载器外，表中其他云加载器和三个通知模块均在 v1.2.0 新增。

Provider 实现 `contracts.Provider` 的 `Name` 和 `Load`。可选的 `Describe`
提供优先级与文件路径；可选的 `contracts.Watcher` 通过 `Watch(ctx, from)` 提供
原生事件。`New` 会独立启动 Provider 监听，不依赖 `WithWatch`。链式方法会修改
Provider，应在注册前配置完毕。Provider 层内，高优先级后覆盖，同优先级按注册顺序。

`WithGenerator` 接受生成编码 `RawLayer` 的 `contracts.Generator` 实现。
生成器在 assembly 中先于 Provider 运行，没有内置监听通道。`WithTransform`
注册的函数随后修改合并树，可通过 `transform.NewChain` 实现配置 schema 迁移。
见[生成器](docs/cookbook/processing/generators.md)与[扩展指南](docs/getting-started/extensions.md)。

### 类型、校验与密钥

以下结构体片段使用 v1.6.0 默认值标签，并需导入 `time`。

文档格式与结构体解码独立：默认 JSON 桥使用 `json` 标签；
`WithDecodeFormat(fastconf.YAML)` 改用 YAML 标签。默认类型钩子转换 duration
字符串和来自 env/INI 的基本类型字符串。v1.6.0 还支持 KV 字符串到标量列表与命名
数字/布尔类型：`"a, b"` 或 `'["a","b"]'` 可填充 `[]string`，`"3"` 可填充
`type Level int`；`{"0":"a","1":"b"}` 这样的连续下标 map 也能转换为列表。
字节切片保留 base64 语义，自定义反序列化器保留自身输入格式。

```go
type Database struct {
    DSN     string        `json:"dsn" fc:"required,secret" desc:"Database connection"`
    Pool    int           `json:"pool" default:"10" fc:"min=1,max=100"`
    Timeout time.Duration `json:"timeout" default:"5s"`
    Hosts   []string      `json:"hosts" default:"primary,replica"`
    Enabled bool          `json:"enabled" default:"true"`
}
```

v1.6.0 的 `default:"…"` 在类型解码前只填充缺失键：`5s` 转为 `time.Duration`，
`primary,replica` 转为列表；显式 false、0 和空字符串保留，null 视为键已存在而不注入默认值。
`Explain` 以 `LayerDefault` 记录注入值的来源。旧 `fc:"default=…"` 仍填充解码后的 Go 零值，
`Defaulter.Defaults` 可提供计算型默认值。字段约束先于 `WithValidate` 和 `WithPolicy`
执行；策略警告允许发布，错误级发现阻止发布。标签约束不遍历集合元素，
此类检查使用自定义 validator。
对指针字段，`required` 检查是否为 nil；`min`、`max` 和 `oneof` 检查指向的值，
并跳过 nil 指针。

v1.6.0 的插值通过以下选项加入 `Load` 或 `New`，需导入
`github.com/fastabc/fastconf/transform`：

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

`${VAR:default}` 提供环境变量兜底，`${VAR:?message}` 要求非空值；字段引用递归解析，
带有环与深度检查。环境变量的值不二次展开。被引用的默认值应放在 base 层，
字面量 `default` 标签在插值与 secret 解析之后注入。

v1.6.0 的 `WithSecretResolvers` 按注册顺序串联 resolver，由第一个识别者处理引用；
`PrefixSecretResolver` 可将应用函数包装为 `kms://` 等前缀 resolver。
`vault.NewResolver` 提供 KV v2 字段引用，可选 `secrets/age` 模块提供 age 解密。
解析结果自动在诊断视图中脱敏，`fc:"secret"` 与 `WithSecretPaths` 也可标记未经 resolver
处理的敏感输入。`Get()`、`Value()` 和显式 `Unredacted()` 仍提供明文。
见[字段元数据](docs/cookbook/processing/field-meta.md)、[类型钩子](docs/cookbook/processing/typed-hooks.md)、
[插值](docs/cookbook/processing/interpolation.md)、[策略](docs/cookbook/processing/policy.md)与
[密钥](docs/cookbook/processing/secrets.md)。

### 从配置类型生成文档（v1.6.0）

通过 `schema.JSONSchema`、`schema.ExampleYAML`、`schema.ExampleEnv`，从配置结构体生成
Draft 2020-12 Schema、带注释的 YAML 与 env 示例。`desc`、`default`、`fc` 元数据
提供描述、默认值、必需字段、约束与密钥标记；接入 `go generate`，让应用配置类型
与版本库中的示例文件使用同一份定义。反向的 YAML → Go 结构体仍可使用 `fastconfgen`。
见[生成指南](docs/cookbook/processing/schema.md)与[可运行示例](examples/schema/README.md)。

### 订阅、预览与恢复

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

订阅在发布后比较提取的标量或整个子树。例如分别订阅 Database 与 Log，日志级别改变
时不会触发数据库重连；`WithEqual` 还可将比较范围缩小为 DSN。回调运行于单写者，须快速
返回，不能同步等待当前 Manager 的重载、Plan、回滚或关闭。耗时资源更新应通知
有界 worker。Override 仅作用于当次重载，不修改来源。

`Plan(ctx)` 返回 `Proposed`、`Diff`、`Validators` 和 `Policies`，不发布状态。
即使 Plan 的 err 为 nil，也要调用 `result.Err()` 检查验证失败与策略拒绝，详细报告仍保留；Reload 会重新读取来源。
`Pause` 抑制监听触发；`Resume` 不重放忽略的事件，暂停批次完成后需显式 Reload。
v1.6.0 中暂停也会跳过周期对账，并向通知 refresh 返回 `ErrPaused`；消费者应保留消息或 NACK，不能按成功 ACK。

v1.5.0 之后新增特性的 API、可运行示例与验证命令，统一列在
[examples 特性索引](examples/README.md#features-added-after-v150)。

`Snapshot().Map/Dump/Diff/Explain` 提供脱敏诊断，来源追溯需启用 `WithProvenance`。
明文诊断使用 `Unredacted().Map/Dump`。`History().List()` 按从旧到新返回历史快照；
回滚重新发布保留的快照，不重跑校验或修复来源。未开启历史时 `History()` 返回 nil。
见[订阅](docs/cookbook/runtime/subscribe.md)、[Plan](docs/cookbook/runtime/plan.md)与
[诊断和历史](docs/cookbook/runtime/introspect.md)。

### 可观测性与应用功能

`WithObserver` 接收 reload、stage、Provider 错误、丢弃与 commit 事件。
通过 `observe.Multi`、`JSONLines`、`Metrics`、`Func` 组合审计和遥测。
`observe.Async` 提供有界异步投递，由拥有者在 Manager 关闭后关闭，并为外部 I/O
自行设置 deadline。日志使用 `WithLogger` 与标准 `slog.Handler` 或可选后端适配器。

多租户为每个租户创建 Manager，并显式选择来源和凭据；`WithTenant` 只标记 cause
与策略输入。特性规则是普通配置字段，通过 `feature.Eval` 依次匹配 target、确定性
百分比 rollout，再返回规则默认值，不属于 OpenFeature SDK Provider。
见[观测](docs/cookbook/observability/observability.md)、[多租户](docs/cookbook/deployment/tenant.md)与
[特性开关](docs/cookbook/features/features.md)。

## CLI 工具

| 命令 | 用途 |
|---|---|
| `fastconfctl` | 离线 `dump`、`diff`、`validate`、`explain` |
| `fastconfd` | HTTP 配置快照、手动重载和 SSE 通知；见 [Sidecar 配方](docs/cookbook/deployment/sidecar.md) |
| `fastconfgen` | 从 YAML 生成 Go 结构体；独立模块 |

按匹配版本从源码安装，或使用 [GitHub Releases](https://github.com/fastabc/fastconf/releases)
中的二进制：

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

`fastconfctl validate` 加载通用 map，不校验应用结构体标签或业务策略。
v1.2.0 起，CLI 诊断默认遮蔽常见密钥名。应用结构体可用 `fc:"secret"` 或 `WithSecretPaths`
标记敏感输入，v1.6.0 还会自动脱敏 resolver 的解析结果。
见[诊断配方](docs/cookbook/runtime/introspect.md)。

v1.3.0 中，diff 的空 `-from` 仅选择 base，忽略 `APP_PROFILE` 和
元数据默认 Profile。fastconfd 的监听地址被占用时会退出。

conf.d 发现层仅接受普通文件（跟随符号链接），单文件及元数据上限均为 4 MiB；
见[发现限制](docs/design/spec.md#discovery-limits)（v1.3.0）。

## 开发与文档

```sh
make build       # Root module and fastconfgen
make test        # Root-module race tests
make example     # Executable examples, including optional integrations
make check       # Repository checks; see tools/README.md
```

- [文档索引](docs/README.md)：按功能查找指南与配方。
- [示例索引](examples/README.md)：完整程序、配置与预期输出。
- [仓库工具](tools/README.md)：模块检查与开发命令。
- [架构](docs/architecture.md)：包职责与依赖方向。
- [性能说明](docs/design/perf.md)：可复现测量与读路径预算。
- [发布指南](RELEASING.md)：统一模块标签与分发流程。
- [API 参考](https://pkg.go.dev/github.com/fastabc/fastconf)：已发布的 Go API。

- [自定义 Provider](docs/cookbook/sources/custom-provider.md)：公开工具与契约测试。

## License

[MIT](LICENSE) · Copyright (c) 2026 FastAbc
