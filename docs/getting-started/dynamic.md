# 动态管理：保留最后一次有效配置

使用 `New[T]` 管理生命周期，`WithWatch` 开启文件监听。每次业务操作调用一次
`cfg := mgr.Get()`，随后只读使用 cfg 及其所有可达对象。错误输入不会发布。

完整程序、配置和错误处理见 [examples/dynamic](../../examples/dynamic/README.md)
及 [main.go](../../examples/dynamic/main.go)。Go 1.24+，从仓库根目录运行：

```sh
cp examples/single-file/app.yaml /tmp/fastconf-app.yaml
go run ./examples/dynamic -file /tmp/fastconf-app.yaml
```

另一个终端依次执行，每次等待程序输出：

```sh
printf 'port: 9090\n' > /tmp/fastconf-app.yaml
printf 'prot: 9091\n' > /tmp/fastconf-app.yaml
```

预期初始化 8080、成功发布 9090、拒绝拼错字段并保留 9090。Ctrl-C 让拥有者
调用 Close 并取消订阅。初始化错误由 New 返回，之后的失败从 Errors 读取；
该错误通道有界，持续过载时不能充当完整审计记录。

订阅发生在发布之后，回调需快速返回；耗时资源更新使用
[有界 worker 示例](../../examples/resource-update/README.md)。同步回调内不能
调用当前 Manager 的 Reload、Plan、Rollback 或 Close。

需要可变副本时应按自己的结构复制，而非只复制最外层结构体：

```go
type Config struct {
    Labels map[string]string
    Hosts  []string
}
func copyConfig(src *Config) Config {
    dst := *src
    dst.Labels = maps.Clone(src.Labels)
    dst.Hosts = slices.Clone(src.Hosts)
    return dst
}
```

片段导入标准库 maps、slices；更深的指针或集合需继续逐层复制。
如需先预览再发布，使用 [Plan 与恢复示例](../../examples/reload/README.md)。
Pause 不清空已经排队的操作，也不锁住配置源。
