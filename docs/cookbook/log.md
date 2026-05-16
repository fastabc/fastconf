# JSON / 结构化日志：zerolog 与 phuslu/log 适配

FastConf 的日志接入完全走标准库 `*slog.Logger` / `slog.Handler`——"用什么后端"由调用方决定，根模块只依赖 `log/slog`，不绑死任何具体 logger。

> **内部风格**：FastConf 自己写日志走 `pkg/flog` 的 fluent 风格（`log.Info().Str("k", v).Msg("...")`），底层仍是注入的 `*slog.Logger`。**这只影响 FastConf 内部调用点的写法**，对调用方完全透明——你写自己的日志想用什么风格，与此无关。如果你也想在自己的代码里用同样风格，可以 `import "github.com/fastabc/fastconf/pkg/flog"` 并 `flog.New(myLogger)`；详见文末「pkg/flog 简介」。

如果你需要 JSON 行式输出，常见有三条路径：

| 场景 | 推荐 | 依赖 |
|---|---|---|
| 只想要 JSON 行，对字段名不挑 | `slog.NewJSONHandler` | 零依赖（标准库） |
| 应用已经在用 zerolog，希望 FastConf 跟着走 | `integrations/log/zerolog` 适配子模块 | 仅在用户 `go get` 时引入 zerolog |
| 应用已经在用 phuslu/log | `integrations/log/phuslu` 适配子模块 | 仅在用户 `go get` 时引入 phuslu/log |

两个适配 sub-module **完全独立 go.mod**——根模块的依赖图永远不知道 zerolog / phuslu 的存在。

---

## 路径 A：标准库 JSON Handler（零依赖）

```go
import (
    "log/slog"
    "os"
    "github.com/fastabc/fastconf"
)

h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(h)),
)
```

输出：

```json
{"time":"2026-05-15T12:34:56Z","level":"INFO","msg":"fastconf reload swap","reason":"watcher","generation":7,"layers":5}
```

若想让字段名与 zerolog 默认（`level` / `message`）一致：

```go
opts := &slog.HandlerOptions{
    ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
        switch a.Key {
        case slog.MessageKey: a.Key = "message"
        }
        return a
    },
}
h := slog.NewJSONHandler(os.Stderr, opts)
```

---

## 路径 B：zerolog 适配（`integrations/log/zerolog`）

```bash
go get github.com/fastabc/fastconf/integrations/log/zerolog@latest
```

```go
import (
    "os"
    "github.com/fastabc/fastconf"
    "github.com/rs/zerolog"
    zerologadapter "github.com/fastabc/fastconf/integrations/log/zerolog"
)

zl := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.InfoLevel)
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(zerologadapter.NewHandler(zl, zerologadapter.Options{}))),
)
```

`Options` 字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `Level` | `slog.Leveler` | 可选 slog 侧门控。`nil`（默认）= 不在 slog 侧过滤，全部由 zerolog 决定 |
| `AddSource` | `bool` | 是否带上调用站点 `file:line` |
| `GroupSeparator` | `string` | `slog.Group` 嵌套时连接键的分隔符，默认 `.` |

`Level` 可以是 `*slog.LevelVar`，实现**热门控**：

```go
lv := new(slog.LevelVar)
lv.Set(slog.LevelInfo)
h := zerologadapter.NewHandler(zl, zerologadapter.Options{Level: lv})

// 运行期把 fastconf 自身日志降到 Debug，而不动 zerolog 全局级别：
lv.Set(slog.LevelDebug)
```

---

## 路径 C：phuslu/log 适配（`integrations/log/phuslu`）

```bash
go get github.com/fastabc/fastconf/integrations/log/phuslu@latest
```

```go
import (
    "os"
    "github.com/fastabc/fastconf"
    plog "github.com/phuslu/log"
    phusluadapter "github.com/fastabc/fastconf/integrations/log/phuslu"
)

pl := &plog.Logger{
    Level:      plog.InfoLevel,
    TimeFormat: "2006-01-02T15:04:05.999Z07:00",
    Writer:     plog.IOWriter{Writer: os.Stderr},
}
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(phusluadapter.NewHandler(pl, phusluadapter.Options{}))),
)
```

`Options` 字段与 zerolog 适配完全一致（`Level slog.Leveler` / `AddSource bool` / `GroupSeparator string`）。

传 `nil` Logger 会得到一个无操作 handler（不 panic、不输出），方便"测试时关闭日志"。

---

## 三种路径选择建议

| 你的场景 | 选什么 |
|---|---|
| 只想要 JSON，没历史包袱 | **A** 标准库，零依赖 |
| 已经在用 zerolog | **B** zerolog 适配 |
| 已经在用 phuslu/log | **C** phuslu 适配 |
| 用其他第三方 logger（zap / logrus / charmbracelet/log …） | 仿照 B/C 的形态自己写一个 `slog.Handler` 适配，放在你自己的项目里 |

无论哪条路径，根 `go.mod` **永远只依赖** `yaml.v3 + json-patch + fsnotify + contracts` 这套最小集；具体 logger 实现由你按需 `go get` 拉入。

---

## slog → 后端字段语义对照

两个适配 sub-module 共享同一套 slog.Attr → 后端 Entry 映射：

| slog.Value Kind | zerolog Entry 方法 | phuslu Entry 方法 |
|---|---|---|
| `KindString` | `Str(k, v)` | `Str(k, v)` |
| `KindInt64` | `Int64(k, v)` | `Int64(k, v)` |
| `KindUint64` | `Uint64(k, v)` | `Uint64(k, v)` |
| `KindFloat64` | `Float64(k, v)` | `Float64(k, v)` |
| `KindBool` | `Bool(k, v)` | `Bool(k, v)` |
| `KindDuration` | `Dur(k, v)` | `Dur(k, v)` |
| `KindTime` | `Time(k, v)` | `Time(k, v)` |
| `KindAny`（error） | `AnErr(k, err)` | `AnErr(k, err)` |
| `KindAny`（其它） | `Interface(k, v)` | `Any(k, v)` |
| `KindGroup` | 嵌套，键以 `GroupSeparator` 串接（如 `stage.name`） | 同左 |

---

## 常见问题

- **为什么不直接把适配放进根模块？** 因为这样会让所有 fastconf 用户（即使不用 zerolog/phuslu）的 `go.sum` 都被污染。Sub-module 独立 `go.mod` 是 FastConf 的一贯隔离原则，与 `observability/otel`、`policy/cue` 同构。
- **能不能同时用两套？** 可以，但通常没有必要。`slog` 接口允许你随时切换 handler；也可以用 `slog.NewLogger(io.MultiWriter(...))` 做多写。
- **Group 为什么用 dotted key 而不是嵌套 JSON 对象？** zerolog/phuslu 都没有原生 group 概念；扁平 + 前缀是最低成本、最不损失语义的映射；如果你需要嵌套，自定义 `slog.Handler` 写 `RawJSON` 即可。

---

## `pkg/flog` 简介

FastConf 内部不再写 `logger.Info("msg", "k", v, "k2", v2, ...)` 这种 slog 默认风格，而是包了一层 fluent builder：

```go
log.Info().
    Str("reason", reason).
    Uint64("generation", gen).
    Int("layers", n).
    Err(err).      // err == nil 自动跳过
    Msg("fastconf reload swap")
```

设计要点：

- **底层仍是 `*slog.Logger`**——`flog.New(slog.New(handler))` 即可。所有上面讲过的 zerolog / phuslu / 标准库 Handler 都直接可用，**调用点不感知后端**。
- **Level 短路 + 池化**：disabled 时 `Info()`/`Debug()` 返回 nil，所有链式方法 no-op，开销只剩一次 level 检查；`Msg()` 时通过 `sync.Pool` 回收 Event，amortized 零分配。
- **强类型字段方法**：`Str / Strs / Int / Int64 / Uint64 / Float64 / Bool / Dur / Time / Err / NamedErr / Any / Attr`；写错类型编译期就拦下。
- **互操作逃生口**：`log.Slog()` 返回底层 `*slog.Logger`，可塞给任意 slog-typed API。
- **Ctx 变体**：`InfoCtx(ctx)` / `DebugCtx(ctx)` 等保留 context 传播。
- **派生 logger**：`log.With().Str("component", "x").Group("stage").Str("name", "decode").Logger()` 等价于 zerolog 的 `With().Str(...).Logger()`。

如果你只关心如何让 FastConf 自己产生 JSON / zerolog 风格的输出——**不需要 import `pkg/flog`**，只要按上面路径 A/B/C 配 Handler 就行。`pkg/flog` 是给"自己也想这么写日志的"调用方准备的。
