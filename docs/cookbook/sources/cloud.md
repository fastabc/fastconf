# 嵌入文件与云端来源

这些入口新增于 v1.1.0 之后。`source.NewFS` 随根模块提供；Parameter Store、
GCS 和 Azure Blob 各有独立 `go.mod`，仅导入所需模块即可，根模块没有新增云 SDK。
最低 Go 版本分别以各模块清单为准。

无需账号或凭据的完整可运行示例：[FS](../../../examples/fs/README.md)、
[Parameter Store](../../../examples/parameterstore/README.md)、
[GCS](../../../examples/gcs/README.md)、[Azure Blob](../../../examples/azblob/README.md)。
云端示例使用真实 SDK 连接进程内的本地 HTTP 服务；`make example` 会运行全部示例。

以下代码是集成片段，省略应用配置类型、imports 和外层函数。云 SDK 客户端应在
应用初始化时配置认证、重试和网络策略；Provider 不修改客户端，也不负责关闭它。

## 单文件 fs.FS

导入 `embed` 和 `github.com/fastabc/fastconf/providers/source`：

```go
//go:embed config/app.yaml
var defaults embed.FS

// 在应用初始化函数内：
p := source.NewFS(defaults, "config/app.yaml")
// 将 fastconf.WithProvider(p) 传给 fastconf.Load 或 fastconf.New。
```

路径相对于文件系统根目录，必须满足 `fs.ValidPath`；绝对路径和 `..` 不合法。
按扩展名选择 codec；缺失文件返回错误，空文件贡献空层。内容哈希作为 revision。
这与加载完整 base/overlay 目录的 `fastconf.WithFS` 不同。

默认优先级为 `contracts.PriorityStatic`，支持 `.WithPriority(n)` 和
`.WithMaxBodyBytes(n)`。没有 OS 监听路径或原生 Watcher；对可变文件系统调用
`Manager.Reload` 会重新打开文件，嵌入文件通常保持不变。

## AWS Parameter Store

模块：[`providers/parameterstore`](../../../providers/parameterstore/go.mod)。
导入 `github.com/fastabc/fastconf/providers/parameterstore`，准备配置好的
`*ssm.Client`（例如由 `ssm.NewFromConfig(awsConfig)` 构造）：

```go
p, err := parameterstore.New(parameterstore.Config{
    Path:    "/myapp/prod",
    Decrypt: true,
}, ssmClient)
if err != nil {
    return err
}
// 将 fastconf.WithProvider(p) 传给 fastconf.Load 或 fastconf.New。
```

使用 `GetParametersByPath` 递归读取所有分页。`/myapp/prod/server/host` 映射为
`server.host` 的嵌套树；参数值保持字符串，不自动拆分 StringList。
参数名中的点保留为字面字符，只有 `/` 是路径分隔符。

`Decrypt` 默认为 false；启用时要求相应的 SecureString/KMS 解密权限。
路径必须为非根绝对路径，允许尾部斜线。空结果贡献空层；分页失败、重复分页 token、
重复参数、非法路径或叶子与分支冲突都使整次加载失败，不发布部分配置。
revision 根据排序后的参数名、值和版本计算，不受分页顺序影响。

## Google Cloud Storage

模块：[`providers/gcs`](../../../providers/gcs/go.mod)。
导入 `github.com/fastabc/fastconf/providers/gcs`，传入应用持有的
`*storage.Client`。客户端可使用应用选择的凭据或 workload identity：

```go
p, err := gcs.New(gcs.Config{
    Bucket: "my-configs",
    Key:    "prod/app.yaml",
}, storageClient)
if err != nil {
    return err
}
```

每次加载查询元数据；generation 未变化时返回已解码缓存的深拷贝，否则固定到
该 generation 下载正文，避免对象更新导致 revision 与内容不一致。
缺失对象是错误，不清空此前已发布的配置。客户端的 `Close` 由应用调用。

## Azure Blob Storage

模块：[`providers/azblob`](../../../providers/azblob/go.mod)。
导入 `github.com/fastabc/fastconf/providers/azblob`，传入配置好的
`*azblob.Client`；客户端可使用 token credential、shared key、SAS 或模拟器：

```go
p, err := azblob.New(azblob.Config{
    Container: "configs",
    Key:       "prod/app.yaml",
}, blobClient)
if err != nil {
    return err
}
```

首次成功解码后保存 ETag，后续使用 `If-None-Match` 条件请求；304 返回缓存的
深拷贝。读取或解码失败不会更新缓存。Provider 名称去除客户端 URL 中的 SAS
查询参数和用户信息；客户端本身保持原样。

## 刷新与限制

三个云 Provider 的默认优先级均为 `contracts.PriorityKV`，可通过 `Config.Priority`
覆盖。GCS/Azure Blob 按 Key 扩展名选择 codec；无扩展名时设置 `Config.Codec`。

默认大小上限为 4 MiB，通过各包的 `WithMaxBodyBytes(n)` 设置，非正数恢复默认值。
FS/GCS/Azure Blob 限制解码前的文档字节数；Parameter Store 限制所有参数名称与值
的累计字节数。超限返回 `contracts.ErrTooLarge`。

三个云 Provider 只实现 `Provider` 和 `Describer`，没有自动轮询或原生 Watcher。
调用 `Manager.Reload(ctx)` 获取新数据；自动刷新由应用调度并传入有界 context。
加载错误遵循 Manager 的[重载失败策略](../runtime/reload-policy.md)。

发布前，在当前 checkout 验证这些新模块：

```sh
make test-workspace MODULES=". providers/parameterstore providers/gcs providers/azblob"
```
