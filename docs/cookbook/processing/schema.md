# 由结构体生成 Schema 与示例 (v1.6.0)

`github.com/fastabc/fastconf/schema` 提供三个函数：

```go
t := reflect.TypeFor[config.Config]()
js, err := schema.JSONSchema(t)              // Draft 2020-12
example, err := schema.ExampleYAML(t)        // 带注释的 YAML
vars, err := schema.ExampleEnv(t, "APP_", "__")
```

输入为结构体或其指针，字段名使用 JSON 标签与嵌入规则。
`desc:"…"` 优先于 `fc:"desc=…"`；`default:"…"` 优先于旧 `fc:"default=…"`。
Manager 会拒绝同一字段的两种默认值标签，生成器读取元数据时使用上述优先级。
反射无法读取源码注释，请用 `desc` 标签。

Schema 支持基础标量、Duration（带 pattern 的字符串）、`[]byte`（base64）、列表、
字符串键 map 和嵌套结构体。`TextUnmarshaler` 映射为字符串，`json.Unmarshaler` 与
interface 映射为空 schema；递归类型重复处使用对象占位。
`required`、数值 `min` / `max`、`oneof`、`secret` 分别映射为 required、数值范围、
enum 和 writeOnly。对象允许额外属性，与默认 UnknownWarn 一致。

YAML/env 按字段声明顺序输出注释：描述后追加 `(required, secret)` 中成立的项。
YAML 无默认值时输出零值，标量列表使用 flow 风格。env 无默认值时输出空值，列表
使用逗号拼接。字段名包含所选分隔符时输出无法寻址的注释并跳过该字段；推荐 `__`，
例如 `APP_DB__MAX_CONNS`。map 的动态键需要应用自行补充。
示例中的 secret 默认值仍会输出，标签中请勿存放真实凭据。

## go generate

在应用配置包中声明：

```go
//go:generate go run ./internal/cmd/genconfig
```

生成器负责选定输出位置与处理错误：

```go
// internal/cmd/genconfig/main.go (imports omitted)
func main() {
    b, err := schema.ExampleYAML(reflect.TypeFor[config.Config]())
    if err != nil { panic(err) }
    if err := os.WriteFile("config.example.yaml", b, 0o644); err != nil { panic(err) }
}
```

完整程序见 [schema 示例](../../../examples/schema/README.md)。
