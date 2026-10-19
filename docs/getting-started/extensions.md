# 扩展：添加一个来源或策略

先选择[一次加载](load.md)或[动态管理](dynamic.md)，再按需求增加扩展。
下面的本地 Provider 和 policy.Func 示例随根模块发布，Go 1.24+ 即可运行，无需外部服务。

## 环境变量覆盖

完整程序与配置：[examples/basic](../../examples/basic/README.md)。
它使用 base → prod overlay → env 的顺序，并检查未知字段和端口范围。

```sh
go run ./examples/basic
FC_EXAMPLE_SERVER_PORT=9090 go run ./examples/basic
FC_EXAMPLE_SERVER_PORT=0 go run ./examples/basic
```

第一条显示 fixture 的 prod 配置，第二条显示 port=9090，第三条返回校验错误。
导入 `github.com/fastabc/fastconf/providers/env`，使用
`fastconf.WithProvider(env.New("FC_EXAMPLE_"))`。Profile 选择变量使用独立前缀，
避免严格解码把它当作未知业务字段。

## 策略检查

完整程序：[examples/reload](../../examples/reload/README.md)。
导入 `github.com/fastabc/fastconf/policy`，以 `WithPolicy(policy.Func[Config]{...})`
添加业务规则。运行 `go run ./examples/reload` 会展示字段校验拒绝、策略拒绝、
策略警告、合法发布、回滚和修复源文件的闭环。

Plan 的 err 为 nil 时仍须检查 Validators 和 Policies；警告允许发布，
SeverityError 阻止发布。Plan 和 Reload 之间会重新读取来源。

## 扩展入口与模块边界

根模块内的扩展无需另装依赖，按使用场景选择：

| 需求 | 包 / API | 说明 |
|---|---|---|
| 附加文件、字节和环境变量 | `providers/source`、`providers/env`、`providers/dotenv` | [来源与分层配方](../cookbook/README.md#sources-and-layering) |
| 显式设置的 CLI 参数 | `providers/cliflag` | `cliflag.New(values)` 接收嵌套 map，不展开 dotted key；pflag 适配器负责转换 |
| 远程配置 | `providers/http`、`providers/vault`、`providers/consul` | 原生监听独立于文件监听开关 |
| 自定义来源 | `contracts.Provider`、可选 `contracts.Watcher` / `Describer` | [所有权与生命周期](../design/spec.md#configuration-and-ownership) |
| 编码文档生成 | `contracts.Generator` / `WithGenerator` | [生成器](../cookbook/processing/generators.md) |
| 合并后变换 | `transform` / `WithTransform` | 包含环境替换、字段别名、删除与 schema 迁移链 |
| 类型与业务校验 | `codec` / `WithTypedHook`、`WithValidate`、`policy.Func` | [转换和校验配方](../cookbook/README.md#merge-conversion-and-validation) |
| 文件输出 | `integrations/render` | 见下文；为读取文件的守护进程生成配置 |

## 可选模块

仅安装应用实际导入的适配器，例如：

```sh
go get github.com/fastabc/fastconf/providers/s3@v1.1.0
```

下表是选型导航；最低 Go 版本与依赖版本以链接的 `go.mod` 为准，
不另维护一份版本表。根模块的 Go 1.24 下限不适用于所有适配器。

| 功能 | 模块 / 清单 |
|---|---|
| S3 对象与事件适配 | [`providers/s3`](../../providers/s3/go.mod)；`s3events` 同属此模块 |
| pflag 显式参数 | [`integrations/cli/pflag`](../../integrations/cli/pflag/go.mod) |
| go-playground 校验 | [`validate/playground`](../../validate/playground/go.mod) |
| CUE 校验和策略 | [`cue`](../../cue/go.mod)；包含 `cuelang` 和 `policy` 包 |
| OPA/Rego 策略 | [`policy/opa`](../../policy/opa/go.mod) |
| Prometheus 指标 | [`observability/metrics/prometheus`](../../observability/metrics/prometheus/go.mod) |
| OpenTelemetry 追踪 | [`observability/otel`](../../observability/otel/go.mod) |
| zerolog 日志 | [`integrations/log/zerolog`](../../integrations/log/zerolog/go.mod) |
| phuslu/log 日志 | [`integrations/log/phuslu`](../../integrations/log/phuslu/go.mod) |
| YAML → Go 结构体生成 | [`cmd/fastconfgen`](../../cmd/fastconfgen/go.mod) |

完整 S3/pflag 运行命令见[可选集成示例](../../examples/README.md#optional-integration-programs)。
仓库内使用 `bash tools/modules.sh exec <command>` 可在临时 workspace 中验证当前
源码，避免误用已发布的根模块。模块发现与发布规则见[仓库工具](../../tools/README.md)。

## 为文件型守护进程输出配置

`integrations/render.Wire` 接受 Manager、`Renderer[T]`、输出路径、选项和 hooks，
注册订阅并立即渲染当前快照。输出先写同目录临时文件，再原子重命名；相同字节跳过
重复写入。渲染/写入失败保留原文件，写入后的 hook 失败不撤销文件或配置快照。

Renderer、写入与后续 hooks 同步执行，会延迟 writer；外部操作需有界。
通过 `Options.OnError` 处理失败；初始渲染失败也走该回调，不一定从 `Wire` 返回。
调用返回的 cancel 取消订阅，在服务退出时关闭 Manager。
完整接口和契约测试位于 [`integrations/render`](../../integrations/render)。
