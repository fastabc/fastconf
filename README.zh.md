# FastConf — 强类型 · 无锁 · Kustomize 风格配置框架

> **语言**: [English](README.md) · 中文

`fastconf` 把 YAML / JSON / TOML、环境变量、命令行参数、远程 KV 与生成器 layer
叠加成一个强类型 Go 结构体，并在热更新时用**单写者 reload loop + `atomic.Pointer`**
安全发布新快照。业务读路径就是一次 `atomic.Pointer.Load()`。

[![Go Reference](https://pkg.go.dev/badge/github.com/fastabc/fastconf.svg)](https://pkg.go.dev/github.com/fastabc/fastconf)
[![CI](https://github.com/fastabc/fastconf/actions/workflows/ci.yml/badge.svg)](https://github.com/fastabc/fastconf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/fastabc/fastconf)](https://github.com/fastabc/fastconf/releases)

> **Status:** pre-public。当前 API 仍以“把语义收准”为第一目标；本文档与
> [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) 描述的是当前真相。

---

## 目录

1. [先看哪一段](#先看哪一段)
2. [一分钟上手](#一分钟上手)
3. [安装](#安装)
4. [核心模型](#核心模型)
5. [核心抽象](#核心抽象)
6. [公开 API 地图](#公开-api-地图)
7. [Option 参考](#option-参考)
8. [Reload 流水线](#reload-流水线)
9. [Profile 与 Overlay](#profile-与-overlay)
10. [Provider 系统](#provider-系统)
11. [Transformer 与 Migration](#transformer-与-migration)
12. [Watch、Subscribe 与 Plan](#watchsubscribe-与-plan)
13. [Provenance、History 与 Rollback](#provenancehistory-与-rollback)
14. [可观测性](#可观测性)
15. [多租户与 Preset](#多租户与-preset)
16. [为什么没有 `GetString("a.b.c")`](#为什么没有-getstringabc)
17. [性能与可靠性](#性能与可靠性)
18. [Sub-module 生态矩阵](#sub-module-生态矩阵)
19. [扩展指南](#扩展指南)
20. [CLI 工具](#cli-工具)
21. [本地开发](#本地开发)
22. [文档地图](#文档地图)
23. [License](#license)

---

## 先看哪一段

| 你要做什么 | 先看这里 |
|---|---|
| 第一次把 FastConf 接进 Go 服务 | [一分钟上手](#一分钟上手) |
| 在 K8s 里读 ConfigMap 并热更新 | [`docs/cookbook/k8s.md`](docs/cookbook/k8s.md) |
| 接 Vault / Consul / 远程 provider | [`docs/cookbook/README.md`](docs/cookbook/README.md) 的 Providers 区 |
| 做 dry-run、解释来源、回滚历史 | [公开 API 地图](#公开-api-地图) + 对应 cookbook |
| 只想查所有 recipe | [`docs/cookbook/README.md`](docs/cookbook/README.md) |

---

## 一分钟上手

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
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
        fastconf.WithProfileEnv("APP_PROFILE"),
        fastconf.WithDefaultProfile("dev"),
        fastconf.WithWatch(true),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer mgr.Close()

    cfg := mgr.Get() // *AppConfig, lock-free, O(1), zero-alloc
    log.Println(cfg.Server.Addr, cfg.Database.Pool)
}
```

目录约定：

```text
conf.d/
  base/
    00-app.yaml
  overlays/
    prod/
      50-overrides.yaml
      _patch.json
```

`APP_PROFILE=prod` 时，FastConf 会按 `base/*` → `overlays/prod/*` 的顺序合并。
默认 decode bridge 走 JSON round-trip，所以现有结构体若只有 `yaml` tag，请补上
`json` tag，或显式选择 `fastconf.WithCodecBridge(fastconf.BridgeYAML)`。

### 三条推荐入口

| 场景 | 推荐组合 | 继续阅读 |
|---|---|---|
| 本地 / 单服务文件配置 | `New + WithDir + Get` | `ExampleNew` / `docs/cookbook/introspect.md` |
| K8s 热更新服务 | `PresetK8s + Subscribe + Errors` | `docs/cookbook/k8s.md` / `docs/cookbook/reload-policy.md` |
| 远程 source / GitOps | `WithProvider + Plan + Provenance` | `docs/cookbook/vault.md` / `docs/cookbook/consul.md` / `docs/cookbook/plan.md` |

单元测试优先用 `PresetTesting`；sidecar 优先用 `PresetSidecar`；需要 region /
zone / host 多轴叠加再看 `PresetHierarchical` 与 `WithMultiAxisOverlays`。

---

## 安装

**作为 Go library**（请把 `@latest` 换为你实际锁定的版本）：

```bash
go get github.com/fastabc/fastconf@latest

# 可选 sub-module（按需）：
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/policy/cue@latest
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/validate/cue/cuelang@latest
go get github.com/fastabc/fastconf/validate/playground@latest
go get github.com/fastabc/fastconf/providers/s3@latest
go get github.com/fastabc/fastconf/providers/s3events@latest
```

**安装 CLI 工具**（Go ≥ 1.26）：

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

**作为预编译二进制**：每个 GitHub Release 都附 OS+arch 矩阵 (`linux/{amd64,arm64}`、
`darwin/{amd64,arm64}`、`windows/amd64`) × 3 个 binary，外加 `SHA256SUMS`。

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
| 强类型读路径 | `mgr.Get().Server.Addr` 由编译器检查，不靠字符串路径 |
| 单写者 reload | fsnotify、provider 事件、手动 `Reload` 都串行进入同一条写路径 |
| 失败安全 | 任一 stage 失败都保留旧 `State[T]`，不会把坏配置发布给业务 |
| Kustomize 风格叠加 | 支持 base / overlay、RFC 6902 patch、`mergeKeys` 策略合并 |
| 可选扩展 | provider、transformer、secret resolver、policy、metrics、tracer 都是 opt-in |

### 源码布局

```
.    (repo root, package fastconf)
  manager.go              Manager[T] 核心：New / Get / Close / Reload / Snapshot
  pipeline.go             runStages[T] + Plan dry-run 入口
  pipeline_stages.go      Stage[T] 实现（Merge / Assemble / Migrate / Transform / Decode / Validate）
  options.go              所有 WithXxx Option + 公开类型
  state.go                State[T] + ReloadCause + Origins/Explain/Lookup + History
  introspect.go           State.Introspect (Keys / Settings / At)
  watch.go / watcher.go   Subscribe、fsnotify、symlink 处理
  provider_watch.go       provider 事件订阅（指数退避 + drop-on-full）
  presets.go              PresetK8s / PresetSidecar / PresetTesting / PresetHierarchical
  registry.go             RegisterProviderFactory / WithProviderByName
  defaults.go             fastconf:"default=…" struct tag + 内置 hook
  secret.go               fastconf:"secret" + SecretRedactor
  feature.go              FeatureRule / Eval / Sub
  field_meta.go           range / enum / required field-meta check
  errors.go               ErrFastConf sentinel + ReloadError
  obs_audit.go / obs_metrics.go / obs_tracer.go   sinks
  tenant.go               TenantManager[T]
  doc.go                  package-level godoc

pkg/                  ← 公开可复用实现原语（可被外部 Provider / Codec 作者 import）
  decoder/            YAML/JSON codec 注册表
  discovery/          conf.d 目录扫描 + _meta.yaml 解析
  feature/            feature flag rule + EvalContext
  flog/               zerolog 风格 fluent wrapper over *slog.Logger
  generator/          contracts.Generator helpers
  mappath/            dotted-path Get/Set/Delete 工具
  merger/             Kustomize 风格 map[string]any 叠加
  migration/          Chain + Step（From/To/Apply）
  profile/            profile 表达式编译器（&/|/!/()）
  provider/           内置 Env / CLI / Bytes / File / Labels Provider
  transform/          Defaults / SetIfAbsent / EnvSubst / DeletePaths / Aliases
  validate/           Validator + ValidatorReport

internal/             ← 私有 helper（Go 编译时 API boundary）
  debounce/  obs/  typeinfo/  watcher/

contracts/            ← 稳定接口：Provider / Codec / Source / Event / Snapshot / Priority

providers/            ← 内置 Provider（vault / consul / http；nats / redisstream 独立 sub-module）
integrations/         ← bus / render / log / openfeature 适配
observability/        ← metrics/prometheus、otel（各自独立 sub-module）
policy/               ← Policy 接口；cue、opa 后端为独立 sub-module
validate/             ← cue/cuelang、playground 校验后端（独立 sub-module）
cmd/                  ← fastconfd（主模块）、fastconfctl、fastconfgen
```

### 依赖方向（CI 强制）

```
fastconf  →  pkg/{discovery,decoder,flog,merger,provider,validate}
          →  internal/watcher
          →  contracts

pkg/* 之间不得相互依赖，白名单例外（与 tools/check-deps.sh 同步）：
  pkg/discovery → pkg/profile
  pkg/generator → pkg/mappath
  pkg/provider  → pkg/decoder
  pkg/provider  → pkg/mappath
  pkg/transform → pkg/mappath
internal/* 之间不得相互依赖；只允许标准库。
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
func (s *State[T]) MarshalYAML(redactor SecretRedactor) ([]byte, error)  // redactor 非 nil 时按 fastconf:"secret" 路径脱敏
func (s *State[T]) Diff(other *State[T]) []string
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
| 文件与 profile | `WithDir`, `WithFS`, `WithProfile`, `WithProfiles`, `WithProfileEnv`, `WithMultiAxisOverlays` |
| 接外部 source | `WithProvider`, `WithProviderOrdered`, `WithProviderByName`, `WithProviderRegistry`, `WithGenerator`, `WithDotEnvAuto` |
| 业务读取 | `Manager.Get`, `Manager.Snapshot`, `Sub` |
| 成功提交后的反应 | `Subscribe`, `WithDiffReporter`, `Manager.Watcher` |
| 失败处理 | `Manager.Errors`, `ReloadError` |
| 预演与诊断 | `Manager.Plan`, `State.Introspect`, `State.Explain`, `State.LookupStrict` |
| 历史与恢复 | `WithHistory`, `Manager.Replay`, `Replay.List`, `Replay.Rollback` |
| 解码与校验 | `WithTransformers`, `WithTypedHook`, `WithSecretResolver`, `WithStructDefaults`, `WithDefaulterFunc`, `WithValidator`, `WithPolicy` |
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

| Option | 说明 | 默认值 |
|---|---|---|
| `WithWatch(bool)` | 启用 fsnotify | `false` |
| `WithCoalesceQuiet(d)` | 每个父目录 burst 触发前的静默窗口 | `30ms` |
| `WithCoalesceMaxLag(d)` | burst 生命周期的硬上限 | `250ms` |
| `WithCoalesceSwapHint(d)` | 检测到 K8s `..data` 原子 swap 后压缩的窗口 | `5ms` |
| `WithCoalesceProfile(p)` | 应用预设：`ProfileK8s`（默认）或 `ProfileLocalDev` | `ProfileK8s` |
| `WithWatchPaths(paths...)` | 额外监视路径 | — |

Watcher 在 **父目录粒度** 做事件合并，多个独立 ConfigMap 互不阻塞。
检测到 `..data` rename/create（K8s 原子 swap 完成信号）时窗口压到
`swapHint`（5ms）而非等满 `quiet` —— 典型 reload 延迟从旧版全局 500ms 降到 ~5–35ms。

### Profile

| Option | 说明 |
|---|---|
| `WithProfile(p string)` | 显式单 profile |
| `WithProfiles(p ...string)` | 多 profile 模式（用 overlay `_meta.yaml.match` 表达式匹配） |
| `WithProfileEnv(name string)` | 从环境变量读取 profile |
| `WithDefaultProfile(p string)` | 环境变量为空时的 fallback |
| `WithProfileExpr(expr string)` | 全局 profile 匹配表达式（覆盖每个 overlay 的默认 membership 逻辑） |

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
    fastconf.WithProvider(provider.NewEnv("APP_")),                              // APP_DATABASE__DSN → database.dsn
    fastconf.WithProvider(provider.NewEnvReplacer("APP_", provider.DotReplacer)),// APP_DATABASE_DSN → database.dsn
    fastconf.WithProvider(provider.NewCLI(cliMap)),                              // 解析过的 CLI 标志
    fastconf.WithProvider(provider.NewDotEnv("APP_", ".env")),                   // 显式 .env 路径
    fastconf.WithProvider(provider.NewLabels(labels, provider.LabelOptions{})),  // Traefik/Docker 标签
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
| `WithDefaulterFunc[T](fn)` | 自定义 `*T` 默认值填充函数 |
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
profileEnv: "APP_PROFILE"     # 读取 profile 的环境变量（优先级低于 WithProfileEnv）
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
    fastconf.WithProfiles("prod", "eu-west", "canary"),
)
```

`WithProfiles` 与 `WithProfile` 互斥；多 profile 模式下每个 overlay 的
`_meta.yaml.match` 用于判断是否包含。

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
| Env         | `provider.NewEnv("APP_")` | `APP_FOO__BAR` → `foo.bar`（双下划线分隔） |
| EnvReplacer | `provider.NewEnvReplacer("APP_", provider.DotReplacer)` | Viper 风格单下划线 → 点 |
| CLI         | `provider.NewCLI(map[string]any)` | 命令行 flag 解析后的 map |
| DotEnv      | `provider.NewDotEnv("APP_", paths...)` | 显式 `.env` 文件路径 |
| Labels      | `provider.NewLabels(labels, provider.LabelOptions{})` | Traefik / Docker 风格 `key=value` 字符串列表 |
| LabelMap    | `provider.NewLabelMap(labels, provider.LabelOptions{})` | K8s annotation 风格 `map[string]string` |

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

事件驱动 reload：与 `providers/s3events`（S3 → EventBridge → SQS）
组合：

```go
import (
    s3prov   "github.com/fastabc/fastconf/providers/s3"
    s3events "github.com/fastabc/fastconf/providers/s3events"
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
| `pkg/provider.Labels` / `LabelMap` | 主模块 | load-only | — | 无 | 无（in-memory） | 无 |
| `providers/http` | 主模块 | ETag + body-hash 轮询 | — | 必填 | 静态 header（Bearer 等） | `no_provider_http` |
| `providers/consul` | 主模块 | blocking query（X-Consul-Index） | — | 可选（Mode KV/Blob） | ACL Token | `no_provider_consul` |
| `providers/vault` | 主模块 | metadata 版本轮询 | — | （JSON，内建） | 静态 Token / `WithAuth` | `no_provider_vault` |
| `providers/nats` | 独立 sub-module | JetStream subscribe | 是 | 必填 | 注入 `nats.Conn` adapter | （由 sub-module 提供） |
| `providers/redisstream` | 独立 sub-module | `XREAD BLOCK` | 是 | 必填 | 注入 `redis.Client` adapter | （由 sub-module 提供） |
| `providers/s3` | 独立 sub-module | load + ETag 短路 | — | 由 key 扩展名推断或显式 | 静态 AWS 凭证 | `no_provider_s3` |
| `providers/s3events` | 独立 sub-module | SQS 长轮询（EventBridge） | — | 无（watch-only） | 静态 AWS 凭证 | `no_provider_s3events` |

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

## Transformer 与 Migration

### Transformer 接口

```go
type Transformer interface {
    Transform(root map[string]any) error
    Name() string
}
```

Transformer 在 merge 完成、decode 之前运行，接收 `map[string]any`，可安全修改树
结构。

### 内置 Transformer（`pkg/transform`）

```go
import "github.com/fastabc/fastconf/pkg/transform"

fastconf.WithTransformers(
    transform.Defaults(map[string]any{                 // 填默认（递归合并，不覆盖已有）
        "server": map[string]any{"timeout": "30s"},
    }),
    transform.SetIfAbsent("server.timeout", "30s"),    // 单值缺省
    transform.EnvSubst(),                              // 替换 ${VAR} / ${VAR:-default}
    transform.DeletePaths("internal.debug"),
    transform.Aliases(map[string]string{               // 旧路径 → 新路径
        "db.url":      "database.dsn",
        "server.port": "server.addr",
    }),
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
        DSN string `json:"dsn" fastconf:"secret"` // 日志 / 快照中自动脱敏
    } `json:"database"`
}

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithStructDefaults[AppConfig](),                 // 启用 default 标签
    fastconf.WithSecretRedactor(fastconf.DefaultSecretRedactor),
)
```

`fastconf:"default=…"` 在 `stageDecode` 之后、`stageValidate` 之前应用，只填充零
值字段。Field-meta（`range=`, `enum=`, `required`）在同阶段检查。

### Migration（模式迁移）

```go
import "github.com/fastabc/fastconf/pkg/migration"

chain := migration.NewChain(
    migration.Step{From: "1", To: "2", Apply: migrateV1toV2},
    migration.Step{From: "2", To: "3", Apply: migrateV2toV3},
)
fastconf.WithMigrations(chain.Migrate)
```

或一次性 inline：

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

---

## Watch、Subscribe 与 Plan

### 文件系统 Watch

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithWatch(true),
    // 默认 ProfileK8s（quiet=30ms / maxLag=250ms / swapHint=5ms）。
    // 本地开发可换 ProfileLocalDev 或单独覆盖某一项：
    fastconf.WithCoalesceQuiet(50*time.Millisecond),
)
// K8s ConfigMap 的 ..data symlink 原子交换由父目录 fsnotify 正确处理。
// 检测到 swap commit 时窗口压到 swapHint（5ms），多 ConfigMap 互不阻塞。
```

### 字段订阅（`Subscribe`）

```go
cancel := fastconf.Subscribe(mgr,
    func(app *AppConfig) *DatabaseConfig { return &app.Database },
    func(old, neu *DatabaseConfig) {
        if old != nil && *old == *neu { return } // 调用方自行 diff
        reconnect(neu.DSN)
    },
)
defer cancel()
```

`Subscribe` 在每次成功 reload 时同步触发（reload goroutine 中执行；`recover()` 隔
离 panic）。长耗时操作请自行 `go func()` 异步。

### 手动触发 & 一次性 override

```go
err := mgr.Reload(ctx,
    fastconf.WithReloadReason("admin-cli"),
    fastconf.WithSourceOverride(map[string]any{
        "server": map[string]any{"addr": ":9999"},
    }),
)
```

### Pause / Resume

```go
mgr.Watcher().Pause()
applyBatchUpdate()
mgr.Watcher().Resume()
```

### Plan — Dry-run

```go
result, err := mgr.Plan().WithHostname("ci-runner-7").Run(ctx)
if err != nil {
    log.Fatal("plan failed:", err)
}
for _, r := range result.Validators {
    if r.Err != nil {
        log.Printf("validator %s failed: %v", r.Name, r.Err)
    }
}
for _, v := range result.Policies {
    log.Printf("[%s] %s @ %s — %s", v.Severity, v.Rule, v.Path, v.Message)
}
```

`Plan` 不更新 `atomic.Pointer`；`SeverityError` 的 policy violation 在 dry-run
中**降级为警告**，便于 CI/CD 把所有问题一次性列完。

---

## Provenance、History 与 Rollback

### Provenance（字段来源追踪）

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvenance(fastconf.ProvenanceFull),
)

origins := mgr.Snapshot().Explain("server.addr")
for _, o := range origins {
    fmt.Printf("layer=%s priority=%d value=%v\n", o.Source.Name, o.Source.Priority, o.Value)
}

// 严格查询（区分"未开启 provenance"和"路径不存在"）
origins, err := mgr.Snapshot().LookupStrict("database.dsn")
```

| Level | 开销 | 能追踪什么 |
|---|---|---|
| `ProvenanceOff` | 零 | 无 |
| `ProvenanceTopLevel` | O(top-level keys) | 每个顶层字段最终来自哪个 layer |
| `ProvenanceFull` | O(leaves) | 每个叶子字段的完整覆盖链 + 每层的原始值 |

### History 与 Rollback

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithHistory(10),
)

history := mgr.Replay().List()        // []*State[T]，从旧到新
target  := history[len(history)-2]    // 上一个版本
_ = mgr.Replay().Rollback(target)
```

`Rollback` 把历史 `*State[T]` 重新发布到 `atomic.Pointer`：不重新执行 pipeline、
不递增 `Generation`，但会触发 Subscribe 回调（调用方自行 filter）。

### Errors（失败事件流）

```go
go func() {
    for re := range mgr.Errors() {
        slog.Error("reload failed", "reason", re.Reason, "err", re.Err, "when", re.When)
    }
}()
```

缓冲 16，drop-on-full；失败保留旧状态的契约不变。

---

## 可观测性

### AuditSink

```go
type AuditSink interface {
    Audit(ctx context.Context, cause ReloadCause) error
}

sink := fastconf.NewJSONAuditSink(os.Stderr) // 内置 JSON-lines 实现
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithAuditSink(sink),       // 多个 sink fan-out
    fastconf.WithAuditSink(remoteSink),
)
// 输出：{"reason":"watcher","at":"2026-05-14T08:00:00Z","revisions":{"vault":"42"}}
```

### MetricsSink

```go
type MetricsSink interface {
    ReloadStarted()
    ReloadFinished(ok bool, dur time.Duration)
    // 可选扩展：ProviderMetricsSink / StageMetricsSink / RenderMetricsSink
}
```

Prometheus 实现在独立 sub-module：

```go
import prommetrics "github.com/fastabc/fastconf/observability/metrics/prometheus"

mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithMetrics(prommetrics.New()))
```

### Tracer（OTel）

默认 noop；OTel SDK 集成在独立 sub-module：

```go
import fastconfotel "github.com/fastabc/fastconf/observability/otel"

tracer := fastconfotel.NewTracer(otel.GetTracerProvider())
mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithTracer(tracer))
```

`-tags fastconf_otel` 启用 span 属性的额外 enrich。

### DiffReporter

```go
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDiffReporter(fastconf.DiffReporterFunc(
        func(ctx context.Context, ev fastconf.DiffEvent) error {
            return slack.Post(ctx, ev.Diff) // 异步执行，不阻塞 reload
        },
    )),
    fastconf.WithDiffReporterQueueCap(128), // 默认 64
)
```

每个 reporter 拥有独立的 bounded-queue worker：

- 入队是非阻塞的；reload 主线程不会被慢 reporter 拖住。
- 队列满时事件被**丢弃**（drop-on-full），并触发
  `MetricsSink.EventDropped("diff-reporter")`。
- 调用 `Manager.Close()` 时 worker 通过 `m.closed` 信号优雅退出，
  `bgWG.Wait()` 保证不留悬挂 goroutine。
- 通过 `WithDiffReporterQueueCap(n)` 调节每个 reporter 的队列深度（默认 64）。

### Policy（策略引擎）

```go
import "github.com/fastabc/fastconf/policy"

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithPolicy(policy.Func[AppConfig]{
        N: "deny-debug-in-prod",
        Fn: func(_ context.Context, in policy.Input[AppConfig]) ([]policy.Violation, error) {
            if in.Config.Env == "prod" && in.Config.Debug {
                return []policy.Violation{{
                    Rule:     "deny-debug-in-prod",
                    Path:     "debug",
                    Message:  "debug mode must be false in prod",
                    Severity: policy.SeverityError, // 中止 reload
                }}, nil
            }
            return nil, nil
        },
    }),
)
```

CUE / OPA 实现在独立 sub-module：`policy/cue`、`policy/opa`。

| Severity | Plan 行为 | Reload 行为 |
|---|---|---|
| `SeverityWarning` | 记录警告，继续 | 记录警告，继续 |
| `SeverityError` | 降级为警告（dry-run 全量收集） | 中止 reload，保留旧状态 |

---

## 多租户与 Preset

### `TenantManager[T]`

```go
tm := fastconf.NewTenantManager[AppConfig]()

mgrA, _ := tm.Add(ctx, "tenant-a",
    fastconf.WithDir("/etc/config/tenant-a"),
    fastconf.WithProfileEnv("TENANT_A_PROFILE"),
)
mgrB, _ := tm.Add(ctx, "tenant-b",
    fastconf.WithDir("/etc/config/tenant-b"),
    fastconf.WithProvider(tenantBVaultProvider),
)

app, err := tm.Get("tenant-a") // *AppConfig, error（fastconf.ErrUnknownTenant）
_ = tm.Remove("tenant-a")      // 调用底层 Manager.Close()
tm.Close()
```

每个 tenant 完全隔离，AuditSink 自动注入 `Cause.Tenant = id`。

### Preset

```go
// K8s ConfigMap 标准部署
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.PresetK8s(fastconf.K8sOpts{
        Dir: "/etc/config", ProfileEnv: "APP_PROFILE", Default: "default", Watch: true,
    }),
    fastconf.WithStrict(false), // 覆盖 Preset 的 strict=true
)

// fastconfd sidecar
fastconf.PresetSidecar(fastconf.SidecarOpts{
    Dir: "/etc/fastconfd", HistoryN: 16, Watch: true, Strict: false,
})

// 测试（hermetic fs.FS + 固定 profile）
fastconf.PresetTesting(fastconf.TestingOpts{
    FS:      memFS,        // fs.FS（如 testing/fstest.MapFS）
    Profile: "testing",
})

// 多轴 overlay：region / zone / host
fastconf.PresetHierarchical(fastconf.HierarchicalOpts{ /* ... */ })
```

---

## 为什么没有 `GetString("a.b.c")`

这是 FastConf 的边界，不是遗漏。

- 业务热路径应该走 `mgr.Get().Server.Addr`：强类型、零反射、零分配。
- CLI / dump / diff 这类动态场景，走 `state.Introspect().Keys()`、`Settings()`、
  `At(path)`。
- 如果你的配置天生没有稳定 schema，也可以直接使用
  `fastconf.New[map[string]any](...)`，只是会主动放弃类型安全。

详见 [`docs/cookbook/introspect.md`](docs/cookbook/introspect.md)。

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

完整基线、命令和解释见 [`docs/design/perf.md`](docs/design/perf.md)。当前契约是：
**热读极轻、reload 可失败但不污染 live state、订阅 fan-out 不阻塞读取**。

---

## Sub-module 生态矩阵

### 主模块内置包（随根模块版本发布，`import` 路径不变）

| 包 | 路径 | 说明 |
|---|---|---|
| contracts | `contracts` | Provider / Codec / Source / Event 接口定义 |
| pkg/* | `pkg/{decoder,discovery,feature,flog,generator,mappath,merger,migration,profile,provider,transform,validate}` | 公开可复用实现原语 |
| internal/* | `internal/{debounce,obs,typeinfo,watcher}` | 编译时 API boundary 私有 helper |
| http        | `providers/http`   | HTTP / SSE Provider（build tag `no_provider_http`） |
| vault       | `providers/vault`  | HashiCorp Vault KV v2（build tag `no_provider_vault`） |
| consul      | `providers/consul` | Consul KV（build tag `no_provider_consul`） |
| policy      | `policy`           | Policy 接口 + Func adapter |
| integrations/bus | `integrations/bus` | 配置变更事件总线 |
| integrations/render | `integrations/render` | 模板渲染扩展 |
| cmd/fastconfd | `cmd/fastconfd`  | Sidecar HTTP + SSE 服务（与主模块同版） |

### 独立 Sub-module（按需 `go get`）

| Sub-module | 路径 | Tag prefix | 主要依赖 |
|---|---|---|---|
| validate/playground | `validate/playground` | `validate/playground/vX.Y.Z` | go-playground/validator |
| prometheus | `observability/metrics/prometheus` | `observability/metrics/prometheus/vX.Y.Z` | prometheus/client_golang |
| otel | `observability/otel` | `observability/otel/vX.Y.Z` | OpenTelemetry SDK |
| cue-policy | `policy/cue` | `policy/cue/vX.Y.Z` | cuelang.org/go |
| opa-policy | `policy/opa` | `policy/opa/vX.Y.Z` | open-policy-agent/opa |
| cue-validate | `validate/cue/cuelang` | `validate/cue/cuelang/vX.Y.Z` | cuelang.org/go |
| log/phuslu | `integrations/log/phuslu` | `integrations/log/phuslu/vX.Y.Z` | phuslu/log |
| log/zerolog | `integrations/log/zerolog` | `integrations/log/zerolog/vX.Y.Z` | rs/zerolog |
| nats provider | `providers/nats` | `providers/nats/vX.Y.Z` | 仅根 module（注入用户的 `nats.Conn`） |
| redis-streams provider | `providers/redisstream` | `providers/redisstream/vX.Y.Z` | 仅根 module（注入用户的 `redis.Client`） |
| s3 provider | `providers/s3` | `providers/s3/vX.Y.Z` | AWS SDK v2（load + ETag 短路，`FromURL` 辅助函数） |
| s3events provider | `providers/s3events` | `providers/s3events/vX.Y.Z` | AWS SDK v2 SQS（EventBridge S3 → SQS watch 伴随模块） |
| openfeature | `integrations/openfeature` | `integrations/openfeature/vX.Y.Z` | OpenFeature SDK |
| cmd/fastconfctl | `cmd/fastconfctl` | `cmd/fastconfctl/vX.Y.Z` | 仅根 module |
| cmd/fastconfgen | `cmd/fastconfgen` | `cmd/fastconfgen/vX.Y.Z` | yaml.v3 |

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
| `/version` | GET  | 当前 State 版本（Hash + Generation） |
| `/config`  | GET  | 当前配置 JSON（secret 已脱敏） |
| `/reload`  | POST | 触发手动 reload；接受 `{"request_id":"…"}` |
| `/events`  | GET  | SSE 流；每次成功 reload 推送 `ReloadCause` JSON |

### `fastconfctl` — 管理 CLI

```bash
fastconfctl snapshot --addr=:8081
fastconfctl reload   --addr=:8081 --request-id=deploy-123
fastconfctl plan     --addr=:8081
fastconfctl rollback --addr=:8081 --generation=42
fastconfctl sources  --addr=:8081
```

### `fastconfgen` — 代码生成器

```bash
fastconfgen generate --input=conf.d/base/00-app.yaml --pkg=config --out=config/config_gen.go
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
| [`docs/cookbook/README.md`](docs/cookbook/README.md) | 所有 recipe 的单一入口 |
| [`docs/design/spec.md`](docs/design/spec.md) | 运行模型、并发与模块边界 |
| [`docs/design/perf.md`](docs/design/perf.md) | 最新 benchmark baseline |
| [`CHANGELOG.md`](CHANGELOG.md) | 变更记录 |
| [`pkg.go.dev`](https://pkg.go.dev/github.com/fastabc/fastconf) | godoc 与 Example |

最常用的 recipe：

- [`k8s`](docs/cookbook/k8s.md) · [`reload-policy`](docs/cookbook/reload-policy.md) · [`plan`](docs/cookbook/plan.md)
- [`vault`](docs/cookbook/vault.md) · [`consul`](docs/cookbook/consul.md) · [`cross-process`](docs/cookbook/cross-process.md) · [`provider-timeouts`](docs/cookbook/provider-timeouts.md)
- [`secrets`](docs/cookbook/secrets.md) · [`features`](docs/cookbook/features.md) · [`openfeature`](docs/cookbook/openfeature.md)
- [`diff-reporter`](docs/cookbook/diff-reporter.md) · [`policy`](docs/cookbook/policy.md) · [`otel`](docs/cookbook/otel.md)
- [`introspect`](docs/cookbook/introspect.md) · [`field-meta`](docs/cookbook/field-meta.md) · [`typed-hooks`](docs/cookbook/typed-hooks.md)
- [`labels`](docs/cookbook/labels.md) · [`strategic-merge`](docs/cookbook/strategic-merge.md) · [`generators`](docs/cookbook/generators.md)
- [`tenant`](docs/cookbook/tenant.md) · [`sidecar`](docs/cookbook/sidecar.md) · [`dump`](docs/cookbook/dump.md) · [`log`](docs/cookbook/log.md)

---

## License
MIT License

Copyright (c) 2026 FastAbc

See [`LICENSE`](LICENSE).