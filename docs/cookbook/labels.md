# Traefik / Docker / K8s 风格 Label 支持

FastConf 支持把扁平的 `dotted.key=value` 标签列表（Traefik / Docker Compose / K8s annotation 风格）展开成嵌套配置子树。两种入口：

| 来源 | 推荐 API | 备注 |
|---|---|---|
| 已经在配置文件里（Compose `deploy.labels` 列表 / K8s `metadata.annotations` map） | `WithTransformers(transform.ExpandLabels(at, to, opts))` | 原地展开，默认删除源 list |
| 来自 Docker engine / K8s controller / CLI `--label` 等外部注入 | `WithProvider(provider.NewLabels(...))` / `WithProvider(provider.NewLabelMap(...))` | 走 provider 优先级，默认 `PriorityK8s`（40）。Traefik / Docker 场景如需覆盖 env，显式 `Priority: PriorityCLI` |
| K8s Downward API `/etc/podinfo/{labels,annotations}` | `providers/k8s.NewDefault()` 或 `k8s.New(k8s.Options{...})` | 自动按 `{"/", "."}` 多分隔符切分 `app.kubernetes.io/name`，分别挂在 `labels.*` / `annotations.*` 下 |

底层共用 `pkg/mappath.ExpandLabels`，可被第三方代码直接复用。

---

## 1. Compose `deploy.labels` 列表 → 嵌套子树（Transformer）

输入：

```yaml
# conf.d/base/00-app.yaml
deploy:
  labels:
    - "traefik.http.services.dummy-svc.loadbalancer.server.port=9999"
    - "traefik.enable=true"
```

代码：

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/transform"
)

cfg, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithTransformers(transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{})),
)
```

结果（merged tree，源 `deploy.labels` 默认被移除）：

```yaml
traefik:
  http:
    services:
      dummy-svc:
        loadbalancer:
          server:
            port: "9999"
  enable: "true"
```

对应业务 struct：

```go
type Cfg struct {
    Traefik struct {
        Enable string `json:"enable"`
        HTTP   struct {
            Services map[string]struct {
                LoadBalancer struct {
                    Server struct {
                        Port string `json:"port"`
                    } `json:"server"`
                } `json:"loadbalancer"`
            } `json:"services"`
        } `json:"http"`
    } `json:"traefik"`
}
```

### Option 速查

| 字段 | 含义 | 默认值 |
|---|---|---|
| `Prefix` | 仅展开以此为前缀的 key（例如 `"traefik."`） | `""`（不过滤） |
| `StripPrefix` | 展开前去掉前缀 | `false` |
| `Separator` | 单分隔符；拆分 key 的字符 | `"."` |
| `Separators` | 多分隔符（按顺序逐级切分），优先于 `Separator`。`{"/", "."}` 让 K8s 推荐标签 `app.kubernetes.io/name` 拆成 `["app","kubernetes","io","name"]` | `nil` |
| `Coerce` | 把 `"true"`/`"42"`/`"3.14"` 转成 bool/int64/float64 | `false`（保留字符串，匹配 Traefik 语义） |
| `KeepSource` | 是否保留 `at` 处的原始 list | `false`（默认删除） |
| `MergeMode` | 与 `to` 处已有子树的合并策略 | `ExpandReplace` |

### `MergeMode` 三种语义

- **`ExpandReplace`**（默认）：用 label 展开的子树**覆盖** `to` 位置；
- **`ExpandOverlay`**：label 值 **赢过**已有同名 key（适合 "label 写最新值"）；
- **`ExpandUnderlay`**：已有同名 key **赢过** label（适合 "label 只补默认值"）。

```go
fastconf.WithTransformers(transform.ExpandLabels("deploy.labels", "traefik",
    transform.LabelExpandOptions{
        Prefix:      "traefik.",
        StripPrefix: true,
        MergeMode:   transform.ExpandOverlay, // label 写最新值
    })),
```

---

## 2. K8s annotation map → 嵌套子树（Transformer）

```yaml
# conf.d/base/00-svc.yaml
metadata:
  annotations:
    traefik.enable: "true"
    traefik.http.routers.api.rule: "Host(`api.example.com`)"
    unrelated.k: "ignored"
```

```go
fastconf.WithTransformers(transform.ExpandLabels("metadata.annotations", "",
    transform.LabelExpandOptions{
        Prefix:      "traefik.",
        StripPrefix: true,
    })),
```

只展开以 `traefik.` 开头的 annotation；`unrelated.k` 被跳过。

---

## 3. 外部注入：Docker engine / K8s controller / CLI（Provider）

如果 label 来自配置文件**之外**（例如代码中从 Docker engine API 拉到 container labels，或从 K8s informer 拿到 service annotations），用 `provider.NewLabels` / `provider.NewLabelMap` 把它当作一个 provider 注入：

```go
// 从 docker engine 拿到的 []string
dockerLabels := []string{
    "traefik.http.services.api.loadbalancer.server.port=8080",
    "traefik.enable=true",
}

cfg, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(provider.NewLabels(dockerLabels, provider.LabelOptions{
        Prefix:      "traefik.",
        StripPrefix: true,
    })),
)
```

```go
// 从 K8s informer 拿到的 map[string]string
annotations := svc.Annotations
cfg, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(provider.NewLabelMap(annotations, provider.LabelOptions{
        Prefix:      "fastconf.io/",
        StripPrefix: true,
        Separator:   "/",
    })),
)
```

Provider 默认优先级是 `PriorityK8s`（40），位于 remote KV 之上、process env 之下——匹配最常见的 "K8s controller 推送 labels/annotations" 用例。Traefik / Docker engine 场景若需要覆盖 env，显式传 `Priority: PriorityCLI`。

### K8s 推荐标签（多分隔符）

K8s 推荐标签形如 `app.kubernetes.io/name`——前缀 `/` 名字、前缀内部 `.`。用 `Separators` 一次性拆成连贯路径：

```go
fastconf.WithProvider(provider.NewLabelMap(svc.Labels, provider.LabelOptions{
    Separators: []string{"/", "."}, // 先 / 后 .
}))
// app.kubernetes.io/name="web" → app.kubernetes.io.name = "web"
// app.kubernetes.io/component   → app.kubernetes.io.component
```

或直接用 `providers/k8s.NewDefault()` 读 Downward API 文件，预设了多分隔符 + 分桶挂载：

```go
import k8s "github.com/fastabc/fastconf/providers/k8s"

fastconf.WithProvider(k8s.NewDefault()) // 读 /etc/podinfo/{labels,annotations}
```

---

## 4. 直接用 `mappath.ExpandLabels`（脱框架使用）

```go
import "github.com/fastabc/fastconf/pkg/mappath"

tree := mappath.ExpandLabels(
    []string{"a.b.c=1", "a.b.d=2"},
    mappath.LabelOptions{},
)
// tree = {"a": {"b": {"c": "1", "d": "2"}}}
```

`ExpandLabels` 也接收 `[]any`、`map[string]string`、`map[string]any` 四种输入，方便从任意 YAML/JSON 解码结果直接喂入。

---

## 5. 常见陷阱

- **value 中含 `=`**：只在**第一个** `=` 处切分；`key=Host(\`a\`)&&Path(\`/x\`)` 不会丢；
- **缺 `=`**：整条静默丢弃，不会致 reload 失败；
- **空 key**（前缀剥离后变空）：静默丢弃；
- **Coerce + 字符串数字**：`Coerce: true` 时 `"9999"` 变 `int64(9999)`，下游 struct 字段需要相应类型——若希望 port 字段为 string，关闭 Coerce；
- **list-of-map 不可展开**：`labels` 必须是 `[]string` 或 `map[string]string` 形态；不接受 `[{key: ..., value: ...}]` 这种 K8s 风格 list-of-object（如需可先用 `WithTransformers` 自定义先转一道）。
