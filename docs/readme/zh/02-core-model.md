# 02 — 核心模型

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
| 强类型读路径 | `mgr.Get().Server.Addr` 由编译器检查，不靠字符串路径 |
| 单写者 reload | fsnotify、provider 事件、手动 `Reload` 都串行进入同一条写路径 |
| 失败安全 | 任一 stage 失败都保留旧 `State[T]`，不会把坏配置发布给业务 |
| Kustomize 风格叠加 | 支持 base / overlay、RFC 6902 patch、`mergeKeys` 策略合并 |
| 可选扩展 | provider、transformer、secret resolver、policy、metrics、tracer 都是 opt-in |

### 源码布局

```
manager.go           — Manager[T] wrapper, MustNew, Subscribe, TenantManager facade
state.go             — State[T], DiffEntry/DiffChange, Dump, SourcePriorityBand
options.go           — WithXxx option builders + ProfileOptions / WatchOptions / CoalesceOptions
aliases.go           — codec, secret, field-meta public facades
errors.go            — public sentinel errors and ReloadError stream
obs.go               — metrics, tracer, audit-sink facades
defaults.go          — WithStructDefaults + Defaulter interface + WithDefaults
feature.go           — FeatureRule extraction + Eval[T,V]
presets.go           — PresetK8s, PresetSidecar, PresetTesting, PresetHierarchical
registry.go          — RegisterProviderFactory + WithProviderByName
bind.go              — WithSource content-type helpers
doc.go               — package-level godoc

internal/manager     — Manager[T] 主体、pipeline、reload loop、watcher
internal/state       — State[T]、history ring、provenance、diff
internal/options     — Options struct 与 deferred provider 解析
internal/secret      — secret-tag 扫描与 resolver
internal/pipeline    — struct defaults + field-meta runner
internal/obs         — metrics/tracer/audit bridge 类型
internal/provenance  — Origin + OriginIndex

pkg/                 ← 公开可复用实现原语（可被外部 Provider / Codec 作者 import）
  decoder/           YAML/JSON codec 注册表
  discovery/         conf.d 目录扫描 + _meta.yaml 解析
  feature/           feature flag rule + EvalContext
  flog/              zerolog 风格 fluent wrapper over *slog.Logger
  generator/         contracts.Generator helpers
  mappath/           dotted-path Get/Set/Delete 工具
  merger/            Kustomize 风格 map[string]any 叠加
  migration/         Chain + Step（From/To/Apply）
  profile/           profile 表达式编译器（&/|/!/()）
  provider/          内置 Env / CLI / Bytes / File / Labels Provider
  transform/         Defaults / SetIfAbsent / EnvSubst / DeletePaths / Aliases
  validate/          Validator + ValidatorReport

contracts/           ← 稳定接口：Provider / Codec / Source / Event / Snapshot / Priority
providers/           ← 内置 Provider（vault / consul / http / nats / redisstream；s3 独立 sub-module）
integrations/        ← bus / render / openfeature；log adapters / pflag 独立 sub-module
observability/       ← metrics/prometheus、otel（各自独立 sub-module）
policy/              ← Policy 接口；OPA 后端为独立 sub-module
cue/                 ← CUE 校验与策略后端（独立 sub-module）
validate/            ← playground 校验后端（独立 sub-module）
cmd/                 ← fastconfd（主模块）、fastconfctl、fastconfgen
```

### 依赖方向（CI 强制）

```
fastconf  →  internal/{manager,options,state,tenant,obs}
          →  pkg/{discovery,decoder,flog,merger,provider,validate}
          →  contracts

pkg/* 之间不得相互依赖，白名单例外（与 tools/check-deps.sh 同步）：
  pkg/discovery → pkg/profile
  pkg/generator → pkg/mappath
  pkg/provider  → pkg/decoder
  pkg/provider  → pkg/mappath
  pkg/transform → pkg/mappath
internal/* 是实现层，可按需依赖同层包；对外只暴露 root facade。
```

---

## 核心抽象

### `Manager[T]` — 配置管理器

```go
type Manager[T any] struct { /* unexported */ }

// 构造（首次 reload 同步执行）
func New[T any](ctx context.Context, opts ...Option) (*Manager[T], error)

// 读路径（lock-free, O(1), zero-alloc）
func (m *Manager[T]) Get() *T

// 写路径（触发 pipeline；等待结果）。ctx 既控制入队/等待，也贯穿 pipeline 自身 ——
// 取消会终止 provider.Load / secret resolver / transformer，并以 ctx.Err() 返回。
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error

// Dry-run（不更新指针；收集全部 ValidatorReport）
func (m *Manager[T]) Plan() *PlanBuilder[T] // .WithHostname(...).Run(ctx) → *PlanResult[T]

// 当前快照（State[T] + Sources + Origins）
func (m *Manager[T]) Snapshot() *State[T]

// 失败事件流（缓冲 16；drop-on-full；Close() 时关闭）
func (m *Manager[T]) Errors() <-chan ReloadError

// 子系统访问器（零成本命名空间）
func (m *Manager[T]) Watcher() *Watcher[T]  // .Pause() / .Resume() / .Paused()
func (m *Manager[T]) Replay()  *Replay[T]   // .List() / .Rollback(*State[T])

// 生命周期
func (m *Manager[T]) Close() error
```

包级泛型函数（“从 `*T` 抽 `M`” 一律走包级）：

```go
// 字段订阅：每次成功 reload 都触发，回调内自行比较 old/new
func Subscribe[T, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M)) (cancel func())

// 强类型 feature flag 评估；类型不匹配返回 def
func Eval[T, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V

// 强类型子树视图（read-only 别名指针）
func Sub[T, M any](s *State[T], extract func(*T) *M) *M
```

### `State[T]` — 不可变快照

```go
type State[T any] struct {
    Value      *T             // 强类型业务结构体（Get() 直接返回）
    Hash       [32]byte       // 全局 SHA-256 指纹
    LoadedAt   int64          // unix nanoseconds
    Sources    []SourceRef    // 参与本次合并的所有 layer
    Generation uint64         // 单调递增版本号
    Cause      ReloadCause    // 触发原因 + Revisions
    // origins: 字段级来源追踪（ProvenanceTopLevel / ProvenanceFull 时填充）
}

func (s *State[T]) Explain(path string) []Origin             // oldest → newest 覆盖链
func (s *State[T]) Lookup(path string) []Origin              // 同 Explain
func (s *State[T]) LookupStrict(path string) ([]Origin, error)
func (s *State[T]) Origins() *OriginIndex
func (s *State[T]) Introspect() *Introspection               // Keys / Settings / At
func (s *State[T]) Redacted() map[string]any                 // 用构造时的 SecretRedactor
func (s *State[T]) Dump(format DumpFormat, redactor SecretRedactor) ([]byte, error) // DumpYAML/DumpJSON/DumpTOML；redactor 非 nil 时按 fastconf:"secret" 路径脱敏
func (s *State[T]) Diff(other *State[T]) []DiffEntry         // 结构化每条路径 diff；行式输出用 FormatDiff
func (s *State[T]) FeatureRules() map[string]feature.Rule
```

### `SourceRef` — Layer 元信息

```go
type SourceRef struct {
    Name     string    // 文件路径 / provider 名称
    Kind     LayerKind // LayerFile / LayerProvider / LayerBytes / LayerCLI / LayerEnv
    Priority int
    LoadedAt int64
}
```

### `ReloadCause` — 触发原因审计

```go
type ReloadCause struct {
    Reason    string            // "initial" / "watcher" / "provider:vault://…" / "manual"
    At        int64             // reload pipeline 启动时间（unix ns）
    Revisions map[string]string // 每个 provider 的 revision（Resumable WatchFrom 用）
    Tenant    string            // TenantManager 多租户标识
}
```

---

## 公开 API 地图

| 需求 | 主要入口 |
|---|---|
| 构造 manager | `New[T]`, `PresetK8s`, `PresetSidecar`, `PresetTesting`, `PresetHierarchical` |
| 文件与 profile | `WithDir`, `WithFS`, `WithProfile(ProfileOptions{...})`, `WithMultiAxisOverlays` |
| 接外部 source | `WithProvider`, `WithProviderOrdered`, `WithProviderByName`, `WithProviderRegistry`, `WithGenerator`, `WithDotEnvAuto` |
| 业务读取 | `Manager.Get`, `Manager.Snapshot`, `Extract` |
| 成功提交后的反应 | `Subscribe`, `WithDiffReporter`, `Manager.Watcher` |
| 失败处理 | `Manager.Errors`, `ReloadError` |
| 预演与诊断 | `Manager.Plan`, `State.Introspect`, `State.Explain`, `State.LookupStrict` |
| 历史与恢复 | `WithHistory`, `Manager.Replay`, `Replay.List`, `Replay.Rollback` |
| 解码与校验 | `WithTransformers`, `WithTypedHook`, `WithSecretResolver`, `WithStructDefaults`, `WithDefaults`, `WithValidator`, `WithPolicy` |
| 可观测性 | `WithAuditSink`, `WithMetrics`, `WithTracer`, `WithDiffReporter`, `WithProvenance` |
| rollout | `WithFeatureRules`, `Eval` |

`pkg.go.dev` 建议阅读顺序：
`New` → `Get` → `Subscribe` / `Errors` → `Plan` → `Replay`。可执行示例：
`ExampleNew`、`ExampleSubscribe`、`ExampleManager_Errors`、
`ExampleManager_Plan`、`ExampleReplay_Rollback`。

---

## Option 参考

所有 `WithXxx` 函数都返回 `Option`，可以任意组合传给 `New[T]`，按调用顺序
last-write-wins 应用。

### 文件系统

| Option | 说明 | 默认值 |
|---|---|---|
| `WithDir(dir string)` | 配置根目录 | `"conf.d"` |
| `WithFS(fs.FS)` | 替代 dir 的 `fs.FS`（测试用） | — |
| `WithStrict(bool)` | 未知字段是否报错 | `false` |
| `WithLogger(*slog.Logger)` | 注入 logger（任何 `slog.Handler` 后端均可） | `io.Discard`（opt-in 才有日志） |
| `WithCodecBridge(BridgeJSON \| BridgeYAML)` | decode bridge | `BridgeJSON` |
| `WithMultiAxisOverlays(axes ...OverlayAxis)` | 多轴 Overlay（region / zone / host 等） | — |
| `WithRawMapAccess(fn)` | decode 前的只读钩子，访问完整 merged map | — |

### Watch

文件监视器只暴露两个桶式 Option：

| Option | 说明 | 默认值 |
|---|---|---|
| `WithWatch(WatchOptions{...})` | 监视开关与路径；字段 `Enabled` / `Paths` / `Coalesce` / `CoalesceProfile` | `Enabled:false` |
| `WithCoalesce(CoalesceOptions{...})` | 仅微调 `Quiet` / `MaxLag` / `SwapHint`，不动 `WithWatch` 已设的字段 | — |

`WatchOptions` 与 `CoalesceOptions` 是普通 struct，字段零值保留框架默认。
`CoalesceProfile` 是预设（默认 `ProfileK8s`，可选 `ProfileLocalDev`），
随后 `Coalesce` 内同名字段会覆盖预设值。

Watcher 在 **父目录粒度** 做事件合并，多个独立 ConfigMap 互不阻塞。
检测到 `..data` rename/create（K8s 原子 swap 完成信号）时窗口压到
`swapHint`（5ms）而非等满 `quiet` —— 典型 reload 延迟从旧版全局 500ms 降到 ~5–35ms。

### Profile

所有 profile 旋钮聚合到一个桶式 Option：

| `ProfileOptions` 字段 | 含义 |
|---|---|
| `Single string` | 显式单 profile |
| `Multi  []string` | 多 profile 模式（用 overlay `_meta.yaml.match` 表达式匹配） |
| `Expr   string` | 全局 profile 匹配表达式（与各 overlay 匹配条件 AND） |
| `EnvVar string` | `Single` / `Multi` 都为空时读取的环境变量 |
| `Default string` | 环境变量为空时的 fallback |

```go
fastconf.WithProfile(fastconf.ProfileOptions{EnvVar: "APP_PROFILE", Default: "dev"})
fastconf.WithProfile(fastconf.ProfileOptions{Multi: []string{"prod", "eu"}})
```

### Source × Parser × Provider

FastConf 把扩展面拆成两条：

- **`Source`**（`pkg/source`）—— 字节流贡献者（file / http / inline bytes）。
  在调用处以 koanf 风格与 **`Parser`**（`pkg/parser`）显式配对，codec 一眼可见。
- **`Provider`**（`pkg/provider`）—— 已结构化的贡献者（env / cli / 一键一值的 KV）。
  无需 Parser。

| Option | 说明 |
|---|---|
| `WithSource(src, parser)` | 绑定 byte-blob Source 与 Parser。Parser 传 `nil` 时按内容类型自动选择 |
| `WithProvider(p)` | 注册已结构化的 provider（核心入口） |
| `WithProviderOrdered(p...)` | 按调用顺序自动分配 `CLI+100, +101, ...`；输入已有非零 Priority 时报错 |
| `WithProviderByName(name, cfg)` | 通过 Factory Registry 按名称构造 provider；解析在所有 Option 应用完之后做 |
| `WithProviderRegistry(r)` | 注入 Manager-local `*ProviderRegistry`；先 local，后全局默认 |
| `WithGenerator(g)` | assemble 阶段动态合成 `[]RawLayer`（如 BuildInfo） |
| `WithDotEnvAuto(prefix)` | 在 `WithDir` 终值上自动发现 `.env` |

`pkg/source` + `pkg/parser` + `pkg/provider` 的工厂函数：

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/parser"
    "github.com/fastabc/fastconf/pkg/provider"
    "github.com/fastabc/fastconf/pkg/source"
    "github.com/fastabc/fastconf/pkg/transform"
)

fastconf.New[Cfg](ctx,
    // byte-blob 层：显式 Source × Parser 配对
    fastconf.WithSource(source.NewFile("/etc/app/config.yaml"), parser.YAML()),
    fastconf.WithSource(source.NewHTTP("https://kv/config"), parser.JSON()),
    fastconf.WithSource(source.NewBytes("inline", "yaml", data), nil), // nil → 内容类型自动绑定

    // 已结构化的 provider —— 无 Parser 槽位：
    fastconf.WithProvider(provider.NewEnv("APP_")),                              // APP_DATABASE_DSN → database.dsn（默认 DotReplacer）
    fastconf.WithProvider(provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)), // 保留单 "_"，仅在 "__" 分级
    fastconf.WithProvider(provider.NewEnv("APP_").At("config.runtime")),         // 把 env 子树挂到指定路径
    fastconf.WithProvider(provider.NewCLI(cliMap)),                       // 仅显式传入的 CLI override
    fastconf.WithProvider(provider.NewDotEnv("APP_", ".env")),                   // 显式 .env fallback 路径
    fastconf.WithProvider(provider.NewDottedLabels(labels, provider.DottedLabelOptions{})), // 显式 dotted config labels
    fastconf.WithProvider(provider.NewRoutingLabels(labels, provider.RoutingLabelOptions{})), // routing DSL labels（typed/list/index 语义）
    fastconf.WithTransformers(transform.ExpandLabels(at, to, opts)),
)
```

### Pipeline 增强

| Option | 说明 |
|---|---|
| `WithMigrations(func)` | 模式迁移回调（在 Transformer 之前） |
| `WithTransformers(t...)` | post-merge / pre-decode 变换链 |
| `WithSecretResolver(r)` | transform 之后、decode 之前解密 leaf 密文 |
| `WithTypedHook(h)` | decode 前重写 leaf（默认含 `time.Duration`） |
| `WithoutDefaultTypedHooks()` | 关闭内置 typed hook 集 |
| `WithStructDefaults[T]()` | 用 struct tag (`fastconf:"default=..."`) 填零值 |
| `WithDefaults[T](fn)` | 自定义 `*T` 默认值填充函数（`Defaulter` 接口的 fn 版） |
| `WithMergeKeys(map)` | Kustomize 风格策略合并（list-of-object） |
| `WithValidator[T](fn)` | decode 后的强类型校验；失败保留旧状态 |
| `WithPolicy[T](p)` | validate 后的策略评估；`SeverityError` 中止 reload |
| `WithFeatureRules[T](extract)` | 把 `feature.Rule` 表挂到 State，供 `Eval` 使用 |

### 可观测性

| Option | 说明 |
|---|---|
| `WithMetrics(MetricsSink)` | 注入 metrics sink（可选扩展 `ProviderMetricsSink / StageMetricsSink / RenderMetricsSink`） |
| `WithAuditSink(AuditSink)` | 每次成功 reload 后回调（多个 sink fan-out） |
| `WithDiffReporter(DiffReporter)` | 每次产生非空 diff 时异步推送；每个 reporter 用独立 bounded-queue + worker，满则丢 + `EventDropped("diff-reporter")` |
| `WithDiffReporterQueueCap(n int)` | 每个 reporter 的队列深度（默认 64） |
| `WithTracer(Tracer)` | OTel 兼容 span tracer |
| `WithProvenance(level)` | `ProvenanceOff` / `ProvenanceTopLevel` / `ProvenanceFull` |
| `WithHistory(n)` | 保留最近 n 个成功状态（History ring） |
| `WithSecretRedactor(r)` | 日志和快照中的 secret 脱敏（与 `WithSecretResolver` 分工：前者只脱敏展示） |

### `ReloadOption`（传给 `Manager.Reload`）

| Option | 说明 |
|---|---|
| `WithSourceOverride(map)` | 注入一次性 override layer |
| `WithReloadReason(s)` | 覆盖默认 `"manual"` 原因，便于审计 |

---
