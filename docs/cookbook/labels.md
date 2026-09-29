# Label 处理：metadata、dotted config 与 routing DSL

FastConf 按来源语义区分 label。先按这条决策树挑入口，然后再读对应小节的细节。

## 0. 快速决策树

```text
你的 labels 来自哪里？
│
├── docker run --label / docker-compose deploy.labels / 你自己的 dotted 应用配置
│       → labels.New(input, labels.Options{})
│         （dotted 展开；值保持 string，Coerce: true 转 typed）
│
├── traefik docker / swarm provider（labels 是路由 DSL）
│       → labels.New(input, labels.Options{Routing: &labels.Routing{
│             EnableGate: "traefik.enable",
│         }})
│         （typed scalar + 逗号 list + [N] index + enable gate）
│
├── K8s Downward API（metadata.labels / annotations 投射到 volume）
│       → providers/k8s.NewDefault()
│         （默认 raw + namespaced，保留 app.kubernetes.io/name 原 key）
│
└── 已经在配置文件里的 deploy.labels: [...] 字段
        → 一个 WithTransform 函数：confmap.ExpandLabels + confmap.Deep（见 §3）
```

**反例**：不要把 K8s `metadata.labels` 喂给 dotted 展开 —— `app.kubernetes.io/name`
会变成嵌套层级，丢失原始 key identity，破坏 selector 语义。

---

## 心智模型一览

| 心智模型 | 推荐 API | 默认语义 |
|---|---|---|
| dotted 配置 labels | `labels.New(input, labels.Options{})` | 值保留 string；默认 `PriorityStatic` |
| 路由 DSL labels | `labels.New(input, labels.Options{Routing: &labels.Routing{}})` | typed scalar + list + `[N]` index；可选整组 enable gate |
| 配置文件里的 dotted label 字段 | `WithTransform` + `confmap.ExpandLabels` | 把已有 list / map 原地展开成配置子树 |
| K8s Downward API metadata | `k8s.NewDefault()` | 默认 raw + namespaced；`WithWatch(Watch{})` 时跟随 projected-volume refresh |

底层都复用 `confmap.ExpandLabels`；区别不在 merge 引擎，而在**调用方表达的意图**。

---

## 1. dotted labels（Provider）

`labels.New` 接受 `[]string` / `[]any`（`"key=value"`）或 `map[string]string` /
`map[string]any`：

```go
import labelprovider "github.com/fastabc/fastconf/providers/labels"

labelInput := []string{
    "server.addr=:9090",
    "feature.rollout=canary",
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(labelprovider.New(labelInput, labelprovider.Options{})),
)
```

```go
annotations := map[string]string{
    "config.server.addr": ":9090",
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithProvider(labelprovider.New(annotations, labelprovider.Options{
        Prefix:      "config.",
        StripPrefix: true,
    })),
)
```

默认落在中性的 `PriorityStatic`（低于文件 overlay 之外的所有 provider band）。
需要 label 覆盖文件时显式提升：

```go
fastconf.WithProvider(labelprovider.New(labelMap, labelprovider.Options{
    Priority: contracts.PriorityK8s,
}))
```

| 字段 | 含义 | 默认值 |
|---|---|---|
| `Name` / `Priority` | provider 名称与优先级 | `"labels:<Prefix>"` / `PriorityStatic` |
| `Prefix` / `StripPrefix` | 只处理某个前缀，必要时展开前移除 | `""` / `false` |
| `Separators` | key 分隔符列表 | `{"."}` |
| `Coerce` | 把 `"true"` / `"42"` / `"3.14"` 转成 typed value | `false` |
| `Routing` | 非 nil 时启用路由 DSL（§2） | `nil` |

---

## 2. 路由 DSL labels

如果这批 label 不是普通 dotted KV，而是一个**路由 DSL**，设置 `Options.Routing`。
它在 dotted 展开之外还会处理：

- `"true"` / `"8080"` / `"1.5"` 这类 typed scalar；
- `web,websecure` 这类逗号 list；
- `domains[0].main` 这类 indexed sibling；
- 可选的整组 gate，例如 `routing.enable=false` 时跳过整组 labels。

```go
labelInput := []string{
    "routing.enable=true",
    "routing.http.services.api.loadbalancer.server.port=8080",
    "routing.http.routers.api.entrypoints=web,websecure",
    "routing.http.routers.api.tls.domains[0].main=example.com",
    "routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithProvider(labelprovider.New(labelInput, labelprovider.Options{
        Routing: &labelprovider.Routing{EnableGate: "routing.enable"},
    })),
)
```

`Routing` 字段：

| 字段 | 含义 | 默认值 |
|---|---|---|
| `EnableGate` | label 存在且值不是 truthy 时跳过整组 | `""`（关闭） |
| `ListSeparator` / `NoListSplit` | list 分隔符及 opt-out | `","` / `false` |
| `KeepRawSuffixes` | 哪些 key suffix 即使含分隔符也保持 raw string | `[".rule", "regexp"]` |
| `Raw` | 全部 value 保持 string，不做 scalar/list 处理 | `false` |
| `LowercaseKeys` | 展开前把完整 key lower-case | `false` |

`Raw` 和 `NoListSplit` 是两个不同的逃生门：前者完全保留 value，后者只关闭
list 拆分，仍保留 scalar coercion。`KeepRawSuffixes` 用于保护表达式型字段，避免
表达式里的逗号被误判成 list。

---

## 3. 配置文件中的 dotted labels（transform）

如果 label 已经在 YAML / JSON 配置里，写一个 transform 原地展开：

```yaml
# conf.d/base/00-app.yaml
deploy:
  labels:
    - "routing.http.services.api.loadbalancer.server.port=9999"
    - "routing.enable=true"
```

```go
expandDeployLabels := func(root map[string]any) error {
    raw, ok := confmap.GetDotted(root, "deploy.labels")
    if !ok {
        return nil
    }
    confmap.DeleteDotted(root, "deploy.labels")
    // Deep(root, tree): label 值赢；Deep(tree, root) 再写回则已有配置赢。
    return confmap.Deep(root, confmap.ExpandLabels(raw, confmap.LabelOptions{}), confmap.Options{})
}

cfg, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithTransform(expandDeployLabels),
)
```

结果：

```yaml
routing:
  http:
    services:
      api:
        loadbalancer:
          server:
            port: "9999"
  enable: "true"
```

---

## 4. K8s metadata：默认 raw + namespaced

K8s labels / annotations 首先是 metadata，不应默认被 reshape 成应用配置。
推荐入口：

```go
import k8s "github.com/fastabc/fastconf/providers/k8s"

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithProvider(k8s.NewDefault()),
    fastconf.WithWatch(fastconf.Watch{}),
)
```

默认结果保留原始 key：

```go
// k8s.metadata.labels["app.kubernetes.io/name"] == "web"
// k8s.metadata.annotations["example.com/rollout"] == "canary"
```

如果业务**明确**要把 metadata key 当配置路径使用，再 opt in：

```go
fastconf.WithProvider(k8s.New(k8s.Options{
    LabelsPath:      "/etc/podinfo/labels",
    AnnotationsPath: "/etc/podinfo/annotations",
    At:              "k8s.metadata",
    LabelsMode:      k8s.MetadataExpanded,
    AnnotationsMode: k8s.MetadataExpanded,
    Separators:      []string{"/", "."},
}))
```

旧的 expanded-root 形状也仍可显式获得：

```go
fastconf.WithProvider(k8s.NewExpandedDefault())
```

---

## 5. 直接用 `confmap.ExpandLabels`

```go
tree := confmap.ExpandLabels(
    []string{"a.b.c=1", "a.b.d=2"},
    confmap.LabelOptions{},
)
// tree = {"a": {"b": {"c": "1", "d": "2"}}}
```

`ExpandLabels` 接受：

- `[]string`
- `[]any`
- `map[string]string`
- `map[string]any`

---

## 6. 常见陷阱

- value 中含 `=`：只在**第一个** `=` 处切分；
- 缺 `=`：整条静默丢弃；
- 空 key：静默丢弃；
- `Coerce: true` 会把字符串数字变成 typed value；
- `labels` 必须是 `[]string` 或 map 形态，不接受 `[{key: ..., value: ...}]`；
- `RoutingLabels` 会默认拆分逗号 list；表达式字段若不在 raw suffix 保护范围内，
  请显式配置 `KeepRawSuffixes` 或 `NoListSplit`；
- K8s metadata 若要保真，请保持默认 raw 模式；不要为了“看起来整齐”先把 selector key 展开掉。
