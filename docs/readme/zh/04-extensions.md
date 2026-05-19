# 04 — 扩展机制

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
    transform.EnvSubst(),                              // 替换 ${VAR} / ${VAR:-default} / ${VAR:?required message}
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
    fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
    // 默认 ProfileK8s（quiet=30ms / maxLag=250ms / swapHint=5ms）。
    // 本地开发可换 ProfileLocalDev 或单独覆盖某一项：
    fastconf.WithCoalesce(fastconf.CoalesceOptions{Quiet: 50 * time.Millisecond}),
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

CUE / OPA 实现在独立 sub-module：`cue/policy`（统一 CUE 模块）、`policy/opa`。

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
    fastconf.WithProfile(fastconf.ProfileOptions{EnvVar: "TENANT_A_PROFILE"}),
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

