# 嵌入文件与云端来源

这些入口新增于 v1.2.0。`source.NewFS` 随根模块提供；云加载器各有独立
`go.mod`，仅导入所需模块即可，根模块没有新增云 SDK。
最低 Go 版本分别以各模块清单为准。

Azure/GCP 模块固定 `golang.org/x/net v0.60.0` 的 HTTP/2 修复，最低 Go 为 1.26；
这包括此前最低 Go 1.25 的 Azure Blob。语法兼容的最低版本不代表安全补丁基线：
本次安全验收使用 Go 1.26.10，部署时应采用修复
[GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) 的受支持工具链版本。

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

## AWS AppConfig

[`providers/appconfig`](../../../providers/appconfig/go.mod) 接收应用配置的
`*appconfigdata.Client`，通过 Data API 读取部署后的内容。完整本地程序见
[examples/appconfig](../../../examples/appconfig/README.md)。

```go
p, err := appconfig.New(appconfig.Config{
    Application: "app-id", Environment: "environment-id", Profile: "profile-id",
    // Codec: "yaml", // 不设置时使用响应 Content-Type
}, appConfigDataClient)
if err != nil { return err }
```

每个 token 仅使用一次；失败且无法确认是否消费 token 时，下次合法轮询重建 session。
`MinimumPollInterval` 默认 60 秒，允许 15 秒到 24 小时的整秒值；实际遵守服务端返回的
更长间隔。提前 `Load` 返回已缓存内容或上次错误，不绕过限流。空响应保留文档；解码失败后
后续空响应仍返回错误，直到收到有效新文档。revision 是内容哈希，不暴露 token。
需要持续调用 Reload，SNS 部署通知不能代替灰度期间的轮询。已有 AppConfig Agent 时，
也可通过 HTTP provider 读取 Agent 的本地端点。

## Azure App Configuration

[`providers/azappconfig`](../../../providers/azappconfig/go.mod) 接收 `*azappconfig.Client`。
[完整程序](../../../examples/azappconfig/README.md) 使用本地 HTTP 服务与真实 SDK。

```go
p, err := azappconfig.New(azappconfig.Config{
    Name: "app-config-prod", Prefix: "app:", Label: "prod",
}, appConfigClient)
if err != nil { return err }
```

Prefix/Label 都是字面匹配，过滤器中的特殊字符会被转义。空 Label 只读无标签设置。
去除 prefix 后，`server:host` 变为嵌套树，点号保留，值保持字符串。
重复 key、空路径段、叶/分支冲突、分页失败均拒绝整次加载。读取完成后按排序后的
key、label、ETag、value 计算 revision；删除 setting 会从下一次完整树中消失。
这不代表服务端多 setting 的事务快照。Key Vault 引用和 App Configuration 特殊
内容类型会被拒绝，不能当作已支持 secret 引用或 feature flag 求值。

## GCP Secret Manager

[`providers/secretmanager`](../../../providers/secretmanager/go.mod) 把一个 secret payload
作为完整配置文档；不是字段级 secret resolver。[完整程序](../../../examples/secretmanager/README.md)
使用真实 REST SDK，并只输出解析后的版本名。

```go
p, err := secretmanager.New(secretmanager.Config{
    Name: "projects/my-project/secrets/app-config/versions/latest", Codec: "json",
}, secretManagerClient)
if err != nil { return err }
```

Name 可用固定版本、latest 或服务支持的 alias；regional resource 需应用选择对应客户端
端点。每次 Reload 都调用 AccessSecretVersion，校验存在的 CRC32C，并以响应的版本资源名
作为 revision。禁用/销毁返回错误，由 Manager 保留上一有效快照；这不等于立即清除进程中
已加载的秘密，应用可自行决定遇到此类错误时停止服务。提供者不自动标注敏感字段，仍须配置
FastConf 的 secret paths/tag；不要直接打印 Snapshot.Map。

这三个加载器默认 PriorityKV、4 MiB 上限，可用 WithMaxBodyBytes 覆盖；AppConfig、
Secret Manager 以及 Azure settings 的 SDK 会先缓冲响应，上限约束的是解码输入/累计字段，
不是传输层内存总量。它们同样不启动 Watch。七个云来源的通知和校准方式见
[云通知配方](cloud-notifications.md)。
