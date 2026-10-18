# 一次加载：单个配置文件

适合启动时读取配置、之后随部署重启的程序。最短路径是 `Load[T]` →
`State.Value()`，无需管理后台任务或调用 Close。

```yaml
# app.yaml
port: 8080
```

```go
package main

import (
    "context"
    "log"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/source"
)

type Config struct {
    Port int `json:"port" yaml:"port" fc:"min=1,max=65535"`
}

func main() {
    state, err := fastconf.Load[Config](context.Background(),
        fastconf.WithProvider(source.NewFile("app.yaml")),
        fastconf.WithUnknownFields(fastconf.UnknownError),
    )
    if err != nil {
        log.Fatal(err)
    }
    cfg := state.Value()
    log.Printf("port=%d", cfg.Port)
}
```

完整受测试程序：[examples/single-file](../../examples/single-file/README.md)。
Go 1.24+，仓库根目录执行：

```sh
go run ./examples/single-file
```

预期 `port=8080`。将键写为 `prot` 或值改成 0 会失败。文件不存在时，
source Provider 返回空层，端口范围校验拒绝零值。

YAML 扩展名决定输入解析器；默认结构体 bridge 使用 JSON 标签。只有 YAML
标签的已有结构体需增加 `WithDecodeFormat(fastconf.YAML)`。例程工作目录没有
`conf.d`；若应用目录存在该目录，其 base/overlay 层也会参与加载，需显式规划
`WithDir` 或使用独立工作目录。

`Value()` 及其 map、slice、pointer 只读。默认值需要保留显式 false、0、空字符串时，
采用 [base 默认层](../cookbook/field-meta.md#missing-zero-and-null)。
运行期间需要更新时转到[动态管理](dynamic.md)。
