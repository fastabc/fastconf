# 一次加载：单个配置文件

适合启动时读取配置、之后随部署重启的程序。调用 `LoadFile[T](path)`，
直接得到 `*T`，无需 context、Provider、State 或 Close。
这个入口新增于 v1.2.0。

```yaml
# app.yaml
port: 8080
```

```go
package main

import (
    "log"

    "github.com/fastabc/fastconf"
)

type Config struct {
    Port int `json:"port" fc:"min=1,max=65535"`
}

func main() {
    cfg, err := fastconf.LoadFile[Config]("app.yaml")
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("port=%d", cfg.Port)
}
```

完整程序：[examples/single-file](../../examples/single-file/README.md)。
Go 1.24+，仓库根目录执行：

```sh
go run ./examples/single-file
```

预期 `port=8080`。将键写为 `prot` 或值改成 0 会失败。文件不存在直接报错，
不会当作可选配置跳过。未知扩展名、解析失败和超过 4 MiB 的文件也会报错。

## 默认行为

- 只读取指定文件，不扫描 `conf.d`，不合并 profile 或环境变量。
- 扩展名选择 YAML / JSON / TOML / INI 解析器；结构体统一使用 `json` 标签。
- 默认拒绝未知字段，自动执行 typed hooks、`fc` 默认值、`Defaults()` 和 `fc` 校验。
- 不启动后台任务；返回值由调用方拥有，不涉及共享快照或关闭操作。

例如 `fc:"default=8080,min=1,max=65535"` 会在字段为零时先填充 8080，
再执行范围校验，因此显式输入 0 也会变成 8080。需要保留 false、0 或空字符串时，
不要在对应字段使用 `default` 标签；把默认值写入配置文件。
详见 [`fc` 标签说明](../cookbook/processing/field-meta.md)。

## 需要更多控制时

`LoadFile` 不接受选项。已有的 `Load[T](ctx, opts...)` 返回 `*State[T]`，
适合多源合并、诊断、自定义校验或只有 YAML 标签的结构体。例如：

```go
state, err := fastconf.Load[Config](ctx,
    fastconf.WithProvider(source.NewFile("app.yaml")),
    fastconf.WithUnknownFields(fastconf.UnknownError),
    fastconf.WithDecodeFormat(fastconf.YAML),
)
```

此片段需导入 `github.com/fastabc/fastconf/providers/source` 并处理 `err`。
`Load` 保持原有语义：默认还会扫描 `conf.d`，`source.NewFile` 将缺失文件视为
空层，`state.Value()` 及其可达对象须保持只读。多层默认值可使用
[base 默认层](../cookbook/processing/field-meta.md#missing-zero-and-null)。
运行期间需要更新时转到[动态管理](dynamic.md)。
