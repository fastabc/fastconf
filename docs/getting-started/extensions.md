# 扩展：添加一个来源或策略

先选择[一次加载](load.md)或[动态管理](dynamic.md)，再按需求增加扩展。
核心 Provider 和 policy.Func 随根模块发布，Go 1.24+ 即可运行，无外部服务。

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

## 外部适配器

只有选定适配器后才安装其独立模块。S3/pflag 等完整运行命令见
[可选集成示例](../../examples/README.md#optional-integration-programs)。
各适配器的最低 Go 版本以其 go.mod 为准；当前 OPA、CUE、OTel、Prometheus、
zerolog、playground 子模块要求 Go 1.26，不能仅按根模块 Go 1.24 判断。
从仓库运行 `bash tools/modules.sh matrix` 可查看发布模块声明。
