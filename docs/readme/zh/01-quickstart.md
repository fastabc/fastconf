# 01 — 快速上手

## 先看哪一段

| 你要做什么 | 先看这里 |
|---|---|
| 第一次把 FastConf 接进 Go 服务 | [一分钟上手](#一分钟上手) |
| 在 K8s 里读 ConfigMap 并热更新 | [`docs/cookbook/k8s.md`](../../cookbook/k8s.md) |
| 接 Vault / Consul / 远程 provider | [`docs/cookbook/README.md`](../../cookbook/README.md) 的 Providers 区 |
| 做 dry-run、解释来源、回滚历史 | [公开 API 地图](#公开-api-地图) + 对应 cookbook |
| 只想查所有 recipe | [`docs/cookbook/README.md`](../../cookbook/README.md) |

---

## 一分钟上手

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

基础文件示例：

```yaml
# conf.d/base/00-app.yaml
server:
  addr: ":8080"
database:
  dsn: "postgres://localhost/app"
  pool: 10
```

带环境变量覆盖运行：

```bash
APP_PROFILE=prod APP_DATABASE_POOL=20 go run .
```

`APP_DATABASE_POOL=20` 会映射到 `database.pool`（默认单 `_` 分隔，Viper /
Spring Boot 风格——若 key 中需要保留字面下划线，改用
`provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)`
切回 `__` 约定）。外部注入的 label `server.addr=:9090` 会映射到
`server.addr`。在这个例子里，env 会覆盖文件中的 `database.pool`，labels 会覆盖
文件中的 `server.addr`。

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

## 从其他配置库迁移

常见 idiom 的快速对照表。

| 你来自 | 原写法 | FastConf 等价 | 关键差异 |
|---|---|---|---|
| **spf13/viper** | `viper.BindPFlag(...)` | `provider.NewCLI(cliflag.FromChanged(cmd.Flags()))` | `BindPFlag` 会把 pflag **默认值** 也注入配置 → 覆盖 YAML / env 的合法值；FastConf 只转发用户显式 set 过的 flag。 |
| **spf13/viper** | 优先级（override > flag > env > config > kv > default） | `Priority*` 常量：`PriorityDotEnv=5` → `PriorityCLI=60`，7 个显式 band | DotEnv 与 K8s 都是 first-class；优先级是每个 provider 自己声明，不是全局开关。 |
| **knadh/koanf** | `k.Load(provider, parser)` — last load wins | `mgr.Add(provider)` + 各 provider 的 `Priority()` | load 顺序**无关**；只看 priority。可随意调换注册顺序。 |
| **knadh/koanf** | `koanf.WithMergeFunc(...)` | `pkg/merger` strategy + `policy/*` sub-module | strategy-driven merge（RFC 6902、mergeKeys 等），通过 option 配置。 |
| **kelseyhightower/envconfig** | `envconfig.Process("APP", &cfg)` | `provider.NewEnv("APP_")` | prefix-based provider，不是 struct tag 扫描。CamelCase 自动拆分（`split_words`）**不支持** — 直接写 dotted key。 |
| **kelseyhightower/envconfig** | `default:"foo"` tag | `merger.Defaults` 层（或 struct 零值） | 默认值在专门的 layer 里，不在 tag。 |
| **kelseyhightower/envconfig** | `required:"true"` tag | `pkg/validate.Required(...)` | validate 是独立 pipeline stage，merge 之后跑。 |
| **caarlos0/env** | `envExpand`（`${VAR}` 插值） | `transform.EnvSubst()`（默认走 `os.Getenv`）或 `transform.EnvSubstWith(lookup func(string) string)`（自定义） | 显式 transformer；想先查 dotenv 再回退 `os.Getenv`，自己写一个 `lookup` 闭包传入。 |
| **joho/godotenv** | `godotenv.Load(".env")` | `provider.NewDotEnv("APP_", ".env")` at `PriorityDotEnv=5` | **不调用 `os.Setenv`** — `.env` 作为 layer 注入，不是副作用。进程 env 仍然覆盖（presence 判定，所以 `APP_PORT=""` 也会 suppress）。 |
| **joho/godotenv** | `godotenv.Overload(".env")`（强制覆盖） | `provider.NewDotEnv(...).WithPriority(contracts.PriorityCLI)` | 用 priority 旋钮替代双 API。 |
| **spf13/cobra + pflag** | `cmd.Flags()` | `cliflag.FromChanged(cmd.Flags())` → `provider.NewCLI(...)` | sub-module `integrations/cli/pflag`，避免 pflag 进入根 module 依赖闭包。 |
| **stdlib `flag`** | `flag.FlagSet` | `cliadapter.FromStdFlag(fs)` → `provider.NewCLI(...)` | 零依赖；在 `pkg/cliadapter`。 |
| **alecthomas/kong** / **urfave/cli** | typed flag struct / `cli.Context` | 用 `cliadapter.From(visit)` + 一行 visit 闭包 | 套路：只遍历 `Changed` / `IsSet` 的 flag 并调 `yield(name, value)`。 |

### 并排：flag 绑定避开 default 泄漏陷阱

Viper 最经典的踩坑：`BindPFlag` 会把 flag 的**默认值**也写进配置 ——
即使用户从没 type 过这个 flag，也会静默覆盖 YAML 或 env 里的真实配置。FastConf
把两件事拆开：

```go
// Viper（容易踩雷）：
//   pflag 默认 "8080" 覆盖 app.yaml 里 server.port: 9090
viper.BindPFlag("server.port", cmd.Flags().Lookup("server.port"))

// FastConf（结构上只接受 changed）：
//   只在用户显式 --server.port 时才生效
import cliflag "github.com/fastabc/fastconf/integrations/cli/pflag"

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(provider.NewCLI(cliflag.FromChanged(cmd.Flags()))),
)
```

### 并排：env 绑定不依赖 struct tag 扫描

```go
// envconfig（struct tag 扫描，一次性）：
type Cfg struct {
    DSN  string `envconfig:"DATABASE_DSN" required:"true" default:"sqlite:///tmp/db"`
    Port int    `envconfig:"SERVER_PORT"  default:"8080"`
}
_ = envconfig.Process("APP", &cfg)

// FastConf（provider layer + 独立 defaults 与 validate）：
type Cfg struct {
    Database struct{ DSN  string } // env: APP_DATABASE_DSN
    Server   struct{ Port int }    // env: APP_SERVER_PORT
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDefaults(Cfg{ /* 零值或填好的默认值 */ }),
    fastconf.WithProvider(provider.NewEnv("APP_")),    // _ → . relaxed binding
    fastconf.WithValidate(validate.Required("Database.DSN")),
)
```

---

## 安装

**作为 Go library**（请把 `@latest` 换为你实际锁定的版本）：

```bash
go get github.com/fastabc/fastconf@latest

# 可选 sub-module（按需）：
go get github.com/fastabc/fastconf/observability/otel@latest
go get github.com/fastabc/fastconf/observability/metrics/prometheus@latest
go get github.com/fastabc/fastconf/cue@latest           # CUE 验证 + 策略
go get github.com/fastabc/fastconf/policy/opa@latest
go get github.com/fastabc/fastconf/validate/playground@latest
go get github.com/fastabc/fastconf/providers/s3@latest
```

**安装 CLI 工具**（Go ≥ 1.22）：

```bash
go install github.com/fastabc/fastconf/cmd/fastconfd@latest
go install github.com/fastabc/fastconf/cmd/fastconfctl@latest
go install github.com/fastabc/fastconf/cmd/fastconfgen@latest
```

**作为预编译二进制**：每个 GitHub Release 都附 OS+arch 矩阵 (`linux/{amd64,arm64}`、
`darwin/{amd64,arm64}`、`windows/amd64`) × 3 个 binary，外加 `SHA256SUMS`。

---

