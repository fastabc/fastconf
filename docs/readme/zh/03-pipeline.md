# 03 — Reload Pipeline

## Reload 流水线

### 触发源

```
                          ┌── fsnotify events → debounce 500ms ──┐
                          │                                       │
Reload(ctx, opts...) ─────┤    reloadCh chan reloadRequest       ├──► reloadLoop
                          │                                       │    (single writer)
provider.Watch events ────┘── backoff + drop-on-full ──────────┘
```

### Pipeline 执行序列

```
reloadCh.recv(req)
  │
  ├─ stageMerge:      discovery.Scan(dir) → decode files → merger.Merge(layers)
  │                   apply _meta.yaml（appendSlices / profileEnv / match）
  │                   apply _patch.json (RFC 6902)
  │
  ├─ stageAssemble:   for each provider: Load(ctx) → merge by Priority
  │
  ├─ stageMigrate:    opts.migrationRun(merged)       [optional]
  ├─ stageTransform:  for each transformer: t.Transform(merged)
  ├─ stageDecode:     json.Marshal(merged) → json.Unmarshal(→ *T)
  │                   apply fastconf:"default=…" struct tags
  ├─ stageFieldMeta:  range / enum / required 检查
  ├─ stageValidate:   for each validator: v(*T)
  ├─ stagePolicy:     for each policy:    p.Evaluate(ctx, *T, reason, tenant)
  │
  └─ commit:
       canonicalHashBytes(mergedJSON) → SHA-256 dedup
       atomic.Pointer.Store(newState)
       history.push(newState)
       for each AuditSink: Audit(ctx, cause)
       fireWatches(oldPartHashes, newPartHashes)
```

### 失败保留语义

任意 stage 返回非 nil 错误时：

- `atomic.Pointer` **不更新**；`Get()` 继续返回旧值；
- `Generation` **不递增**；
- 错误通过 `Reload(ctx).err` 同步返回；同一条事件也通过 `Errors()` 异步广播；
- **AuditSink 不调用**（只有 commit 成功才触发 Audit）；
- `MetricsSink.ReloadFinished(ok=false, dur)` 被调用。

### Context 传播

`Reload(ctx)` 的 `ctx` 不止控制入队/等待 —— 它会被串到执行中的 pipeline：

- `assemble` 入口处 `ctx.Err()` 早退；
- 每个 `provider.Load(ctx)` 共享同一个 ctx，慢 provider 因 ctx 取消而立刻返回；
- 取消产生的错误以 `context.Canceled` / `context.DeadlineExceeded` 原样返回
  （**不**被 `ErrDecode` 包裹），调用方可以 `errors.Is(err, context.Canceled)`
  做精确判断。

文件系统 watcher 与 provider watcher 自身没有 caller ctx，框架在这两条路径上自动
使用 `context.Background()`，保持原有"事件驱动 reload 不被外部干涉"的语义。

---

## Profile 与 Overlay

### 目录结构

```
conf.d/
  base/                   # 所有 profile 共享的基础值
    00-defaults.yaml
    10-feature-flags.yaml
  overlays/
    dev/                  # 仅当 profile == "dev" 时叠加
      50-dev.yaml
    prod/
      50-prod.yaml
      _meta.yaml          # profile 匹配表达式
      _patch.json         # RFC 6902 patch
    staging/
      50-staging.yaml
      _meta.yaml
```

### `_meta.yaml` 字段

```yaml
schemaVersion: "1"
profileEnv: "APP_PROFILE"     # 读取 profile 的环境变量（优先级低于 WithProfile{EnvVar}）
defaultProfile: "dev"         # 兜底 profile
appendSlices: true            # slice 字段追加而非覆盖
match: "prod | staging"       # 布尔 profile 表达式（&, |, !, () 均支持）
```

`match` 由 `pkg/profile` 编译，语法：

| 语法 | 含义 |
|---|---|
| `prod` | profile 集合包含 `"prod"` |
| `prod \| staging` | 包含 prod 或 staging |
| `prod & !debug` | 包含 prod 且不包含 debug |
| `(eu-west \| eu-east) & !debug` | 复合表达式 |

### RFC 6902 JSON Patch

在任意 overlay 目录下放 `_patch.json`，FastConf 会在该层文件叠加完成后应用：

```json
[
  { "op": "replace", "path": "/server/addr",     "value": ":8443" },
  { "op": "add",     "path": "/feature/darkMode","value": true },
  { "op": "remove",  "path": "/legacy/key" }
]
```

### 多 Profile 模式

```go
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProfile(fastconf.ProfileOptions{
        Multi: []string{"prod", "eu-west", "canary"},
    }),
)
```

`ProfileOptions.Single` 与 `.Multi` 互斥 —— 只设其一即可；多 profile 模式下
每个 overlay 的 `_meta.yaml.match` 用于判断是否包含。

---

## Provider 系统

### 内置 byte-blob Source（`pkg/source`）

每个 Source 通过 `WithSource(src, parser)` 与 Parser 配对。Parser 传 `nil`
时按内容类型提示自动绑定（文件扩展名 / HTTP `Content-Type` / `ContentType` 构造参数）。

| Source | 构造 | 说明 |
|---|---|---|
| File  | `source.NewFile(path)` | load 时读文件；内容类型来自扩展名 |
| HTTP  | `source.NewHTTP(url)` | 带 ETag 条件 GET 短路；内容类型来自 `Content-Type` 头 |
| Bytes | `source.NewBytes(name, contentType, data)` | 内存 layer（测试最常用） |

### 内置 Parser（`pkg/parser`）

| Parser | 声明的 content-type |
|---|---|
| `parser.YAML()` | `yaml` / `.yaml` / `.yml` / `application/yaml` / `application/x-yaml` / `text/yaml` |
| `parser.JSON()` | `json` / `.json` / `application/json` / `text/json` |
| `parser.TOML()` | `toml` / `.toml` / `application/toml` / `text/toml` |

第三方 Parser 通过 `parser.Register` 注册自己的 content-type。

### 内置结构化 Provider（`pkg/provider`）

它们直接返回 `map[string]any` —— 无需 Parser。

| Provider | 构造 | 说明 |
|---|---|---|
| Env         | `provider.NewEnv("APP_")` | 默认 `DotReplacer`：`APP_FOO_BAR` → `foo.bar`（单 `_`，Viper / Spring 风格）；值保持字符串，typed decoder 转换。链式 `.WithReplacer(DoubleUnderscoreReplacer)`、`.At("path")`、`.WithCoerce(true)` |
| CLI         | `provider.NewCLI(map[string]any)` | 仅传入用户显式设置的 flag；不要把 parser 默认值塞进来，否则会无意覆盖文件/env |
| DotEnv      | `provider.NewDotEnv("APP_", paths...)` | 显式 `.env` fallback 路径；实际进程 env 即使被设成 `""` 也优先。与 `NewEnv` 同样支持 replacer / `At` / `WithCoerce` |
| Labels      | `provider.NewLabels(labels, provider.LabelOptions{})` | 低层 flat-label primitive；默认 `PriorityStatic`，需要更高优先级时由调用方显式指定 |
| DottedLabels| `provider.NewDottedLabels(labels, provider.DottedLabelOptions{})` | 显式 dotted-config labels；当 key path 本身就是全部 DSL 时使用 |
| RoutingLabels| `provider.NewRoutingLabels(labels, provider.RoutingLabelOptions{})` | routing DSL labels：支持 typed scalar、逗号 list、`[N]` index、可选 enable gate。若输入是 Traefik-style，再显式配置对应的 `Prefix` / `EnableGate` / `LowercaseKeys` |
| LabelMap    | `provider.NewLabelMap(labels, provider.LabelOptions{})` | 低层 primitive 的 `map[string]string` 变体 |
| K8s Downward| `k8s.NewDefault()`（`providers/k8s`） | 读取 `/etc/podinfo/{labels,annotations}`，默认 raw metadata 并挂到 `k8s.metadata.*`；启用 `WithWatch(WatchOptions{Enabled: true})` 时 mounted files 会自动接入统一 fs watcher。只有确实要配置式展开时才用 `NewExpandedDefault()` / `MetadataExpanded` |

### 内置 KV Provider（`providers/{vault,consul,http}`，主模块内）

```go
import (
    vault    "github.com/fastabc/fastconf/providers/vault"
    consul   "github.com/fastabc/fastconf/providers/consul"
    httpprov "github.com/fastabc/fastconf/providers/http"
)

vp, _ := vault.New("https://vault.svc", "kv/data/myapp", os.Getenv("VAULT_TOKEN"))
cp, _ := consul.New("http://consul.svc:8500", "config/myapp")
hp, _ := httpprov.New("remote", "https://example.com/cfg.yaml", yamlCodec{})
```

编译时裁剪（瘦身二进制）：

```bash
go build -tags no_provider_vault,no_provider_consul,no_provider_http ./...
```

### 独立 sub-module Provider

按需 `go get`，不污染根模块依赖图。

```go
// AWS S3 — load + ETag 短路 + 显式静态凭证
import s3prov "github.com/fastabc/fastconf/providers/s3"

sp, _ := s3prov.New(s3prov.Config{
    Region:    "us-east-1",
    Bucket:    "my-configs",
    Key:       "prod/app.yaml",        // 按 ".yaml" 自动选择 codec
    AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    // VersionID: "abc...",            // 固定到特定对象版本
    // Endpoint:  "http://minio:9000", PathStyle: true,  // MinIO / LocalStack
})
mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithProvider(sp))
```

S3 provider 在首次成功 Load 之后记住 ETag，后续 Load 自动带
`If-None-Match`；AWS 返回 304 时直接返回缓存解码结果，跳过 decode
阶段——重复 `Reload()` 几乎免费，对齐 `providers/http` 的"无虚假
reload"契约。

"provider 地址作为配置字段"的 GitOps 写法，使用 `FromURL`：

```go
cfg, _ := s3prov.FromURL(
    "s3://my-configs/prod/app.yaml?region=us-east-1",
    s3prov.Credentials{
        AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
        SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    },
)
sp, _ := s3prov.New(cfg)
```

`FromURL` 支持 `region`、`codec`、`endpoint`、`path_style`、
`version_id`、`priority` 查询参数；凭证通过单独的 `Credentials`
传入，secret 不会出现在可能被日志/配置文件捕获的 URL 里。

事件驱动 reload：与 `providers/s3/s3events`（S3 → EventBridge → SQS）
组合：

```go
import (
    s3prov   "github.com/fastabc/fastconf/providers/s3"
    s3events "github.com/fastabc/fastconf/providers/s3/s3events"
)

loader, _ := s3prov.New(s3prov.Config{ /* ... */ })
notifier, _ := s3events.New(s3events.Config{
    Region:    "us-east-1",
    QueueURL:  "https://sqs.us-east-1.amazonaws.com/123/cfg-events",
    Bucket:    "my-configs",
    KeyPrefix: "prod/",                // 可选过滤
    AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
})

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithProvider(loader),
    fastconf.WithProvider(notifier),   // watch-only：Load 返回空 map
)
```

notifier 用长轮询取 SQS 消息，按 bucket + key prefix 过滤
EventBridge 信封，匹配后 ACK（DeleteMessage）并发出
`contracts.Event`，触发 Manager reload；ETag 短路保证同 bucket
其他 key 变更触发的 reload 不会重新 decode。

NATS JetStream (`providers/nats`) 与 Redis Streams
(`providers/redisstream`) 是事件驱动 provider，调用方通过小接口
注入已有的 `nats.Conn` / `redis.Client` adapter——它们不引入
上游客户端依赖。

### Provider 能力矩阵

30 秒挑出合适模块。"Watch" 是原生变更通知机制；"Resumable" 表示
provider 实现了 `contracts.Resumable.WatchFrom`，断线重连后不丢事件；
"Codec" 列说明是否需要调用方自行选择编解码器。

| Provider | 模块位置 | Watch 模型 | Resumable | Codec | 鉴权 | Build tag |
|---|---|---|---|---|---|---|
| `pkg/provider.Env` / `EnvReplacer` | 主模块 | load-only | — | 无 | env-var prefix | 无 |
| `pkg/provider.CLI` | 主模块 | load-only | — | 无 | 无（in-memory） | 无 |
| `pkg/provider.File` | 主模块 | load-only | — | 按扩展名推断 | 文件系统 | 无 |
| `pkg/provider.Bytes` | 主模块 | load-only | — | 显式 | 无（in-memory） | 无 |
| `pkg/provider.DotEnv` | 主模块 | load-only | — | 无 | 文件系统 | 无 |
| `pkg/provider.Labels` / `LabelMap` / `DottedLabels` / `RoutingLabels` | 主模块 | load-only | — | 无 | 无（in-memory） | 无 |
| `providers/http` | 主模块 | ETag + body-hash 轮询 | — | 必填 | 静态 header（Bearer 等） | `no_provider_http` |
| `providers/consul` | 主模块 | blocking query（X-Consul-Index） | — | 可选（Mode KV/Blob） | ACL Token | `no_provider_consul` |
| `providers/vault` | 主模块 | metadata 版本轮询 | — | （JSON，内建） | 静态 Token / `WithAuth` | `no_provider_vault` |
| `providers/nats` | 独立 sub-module | JetStream subscribe | 是 | 必填 | 注入 `nats.Conn` adapter | （由 sub-module 提供） |
| `providers/redisstream` | 独立 sub-module | `XREAD BLOCK` | 是 | 必填 | 注入 `redis.Client` adapter | （由 sub-module 提供） |
| `providers/s3` | 独立 sub-module | load + ETag 短路 | — | 由 key 扩展名推断或显式 | 静态 AWS 凭证 | `no_provider_s3` |
| `providers/s3/s3events` | 主模块包 | SQS 长轮询（EventBridge） | — | 无（watch-only） | 静态 AWS 凭证 | `no_provider_s3events` |

注意事项：

- *load-only* provider 每次 `Reload(ctx)` 都会贡献一个 layer，但不会
  主动推变更事件。若需要事件驱动 reload，请与 Manager 级触发源
  （`mgr.Watcher()`、fsnotify、外部调度器）或同源的事件 provider
  组合使用。
- *Resumable* provider 在重连时从上次观测到的 `Event.Revision` 续订；
  非 Resumable 的 Watch provider 每次重连冷启动（语义仍然正确，但在
  网络抖动下噪声更大）。
- Build tag 在二进制层面剔除 provider；sub-module 通过不
  `go get` 同样实现剔除。

### `contracts.Provider` 接口

```go
type Provider interface {
    Name()     string
    Priority() int
    Load(ctx context.Context) (map[string]any, error)
    Watch(ctx context.Context) (<-chan Event, error) // 无能力的 provider 返回 (nil, nil)
}
```

### Priority 常量

合并顺序由 `Priority()` 数值升序决定 —— 数值越大，越后合并，越能覆盖：

| 常量 | 数值 | 用途 |
|---|---:|---|
| `PriorityDotEnv` | 5 | `.env` 兜底（最低） |
| `PriorityStatic` | 10 | 静态 / 文件层默认 |
| `PriorityOverlay` | 20 | overlay providers |
| `PriorityKV` | 30 | Vault / Consul / HTTP / NATS / Redis-Streams |
| `PriorityK8s` | 40 | K8s ConfigMap / Secret |
| `PriorityEnv` | 50 | 进程环境变量 provider |
| `PriorityCLI` | 60 | 命令行 flag provider（最高） |

如果不想思考 Priority，用 `WithProviderOrdered(p1, p2, p3)` — 它把传入的
providers 按调用顺序分配 `PriorityCLI+100, +101, +102 …`，最后一个传入的赢；
若某个输入 provider 已显式设置非零 Priority，会直接报错，避免静默覆盖。

### Resumable（断点续订）

```go
type Resumable interface {
    // lastRev 为空时等价于 Watch（冷订阅）。
    // lastRev 非空时从该 revision 之后的变更开始推送。
    // 若 revision 已被压缩，返回 ErrResumeUnsupported，框架回退到冷订阅。
    WatchFrom(ctx context.Context, lastRev string) (<-chan Event, error)
}
```

框架自动记忆每个 provider 最后观测到的 `Event.Revision`，在断线重连时传给
`WatchFrom`。

### Provider Factory Registry

```go
// 注册（通常在 init() 或 TestMain 中）
fastconf.RegisterProviderFactory("vault", func(cfg map[string]any) (contracts.Provider, error) {
    addr, _ := cfg["addr"].(string)
    path, _ := cfg["path"].(string)
    token, _ := cfg["token"].(string)
    return vault.New(addr, path, token)
})

// 使用（让 provider 配置自己来自 YAML）
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithProviderByName("vault", map[string]any{
        "addr":  "https://vault.svc",
        "path":  "kv/data/myapp",
        "token": os.Getenv("VAULT_TOKEN"),
    }),
)
```

Manager-local registry（多租户 / 测试隔离）：

```go
local := fastconf.NewProviderRegistry()
local.Register("scoped", func(cfg map[string]any) (contracts.Provider, error) {
    return myProvider(cfg)
})

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithProviderRegistry(local),
    fastconf.WithProviderByName("scoped", map[string]any{...}),
)
// 全局默认 registry 不被污染；同名 factory local 优先；只在全局存在的名字仍可解析。
```

---

