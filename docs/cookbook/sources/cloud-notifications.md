# 云通知与可靠刷新

本页描述当前 checkout 新增、尚未发布的独立通知模块。加载器负责读取完整配置，通知消费者只调用同步刷新回调；应用通过回调调用 `mgr.Reload`，返回成功后才确认消息。不要在回调中另起 goroutine 后立即返回。

| Provider | 加载模块 | 通知模块 | 云端链路 |
|---|---|---|---|
| AWS AppConfig | `providers/appconfig` | `integrations/notify/sns` | deployment SNS extension → SNS → SQS |
| AWS S3 | `providers/s3` | `integrations/notify/sns` | S3 event notification → SNS → SQS，或 EventBridge → SQS |
| AWS Parameter Store | `providers/parameterstore` | `integrations/notify/sns` | EventBridge → SNS → SQS |
| Azure App Configuration | `providers/azappconfig` | `integrations/notify/azservicebus` | Event Grid → Service Bus topic/subscription |
| Azure Blob | `providers/azblob` | `integrations/notify/azservicebus` | Event Grid → Service Bus topic/subscription |
| GCP Secret Manager | `providers/secretmanager` | `integrations/notify/pubsub` | secret notification topic → Pub/Sub subscription |
| GCS | `providers/gcs` | `integrations/notify/pubsub` | bucket notification → Pub/Sub subscription |

加载 API、值类型、版本固定与客户端认证见[云来源](cloud.md)。SDK 客户端由应用创建、配置和关闭，FastConf 不创建云资源。AWS SDK 的凭据链可以通过现有 `s3.NewWithClient` 使用，不必把静态 AK/SK 放入配置。

## 应用生命周期与恢复

1. 部署时先建立队列/订阅、发布授权、有限重试与死信策略，再启动应用，覆盖首次加载与开始消费之间的窗口。
2. 创建 SDK 客户端，加载 Manager 的初始配置，然后用 `refresh := func(work context.Context) error { return mgr.Reload(work) }` 启动 `consumer.Run(ctx, refresh)`。`Reload` 带可变参数，因此需要这个小包装。
3. 同时运行有界、带抖动的周期 Reload。建议从 60 秒起步，按服务配额、实例数和时效要求调整；AppConfig 还会遵守服务端下一次轮询时间。
4. `Run` 的接收错误返回给应用。应用记录安全错误并带退避重启；刷新、解析失败通过 `Config.OnError` 报告，未配置时写入 `slog`。SQS 删除与 Service Bus settlement 失败也走此回调；Pub/Sub 的异步 Ack/Nack 失败无法通过该回调观察，见下文。持续鉴权错误需要告警，不能只无限静默重启。
5. 关闭时取消消费与校准 context，等它们退出，再关闭 Manager 和应用拥有的客户端。

[cloud-reload 示例](../../../examples/cloud-reload/README.md)提供可运行的消费重启、定期校准与退出流程；通过故意漏掉唯一一次更新通知，证明定期校准可以恢复。

每个独立 Manager 都需要自己的广播接收域：SQS queue、Service Bus subscription 或 Pub/Sub subscription。两个实例共用一个接收域会竞争消息，不能保证都收到通知；可由应用控制面可靠广播，或者明确接受其他实例等定时校准的延迟。云端筛选尽量精确，消费者仍检查资源。使用专用队列/订阅：已知不相关的消息会被确认，错误格式会重投/进入死信。

通知只表示“再读一次当前来源”。重复与乱序消息不会直接写入旧值；配置无变化时 Manager 不重复发布。非法配置、访问错误和校验失败保持上一份有效配置。ACK 表示一次 Reload 成功，不表示事件与云端读取具有事务一致性；可见性延迟、部署灰度和漏通知仍需定期校准。

## AWS：SNS 经 SQS 消费

```go
import notify "github.com/fastabc/fastconf/integrations/notify/sns"

consumer, err := notify.New(notify.Config{
    QueueURL: queueURL, TopicARN: topicARN,
    Source: notify.S3, Bucket: "config", Key: "prod/app.yaml",
    RefreshTimeout: 30 * time.Second,
}, sqsClient) // *sqs.Client，认证与重试由应用配置
// 检查 err 后，在应用管理的 goroutine 内运行：
refresh := func(work context.Context) error { return mgr.Reload(work) }
err = consumer.Run(ctx, refresh)
```

其他来源将 selector 替换为：

```go
notify.Config{QueueURL: queueURL, TopicARN: topicARN, Source: notify.AppConfig,
    Application: applicationID, Environment: environmentID, Profile: profileID}
notify.Config{QueueURL: queueURL, TopicARN: topicARN, Source: notify.ParameterStore,
    Path: "/myapp/prod"}
```

部署步骤：

- 建立 Standard SNS topic，每个接收域建立 SQS queue 并订阅。设置 SNS 发布权限及 SQS 的 `sqs:SendMessage` 资源策略，限制来源 topic ARN。RawMessageDelivery 与普通 SNS envelope 均支持；`TopicARN` 只校验 envelope，raw 来源真实性依赖队列策略。
- S3 配置 `s3:ObjectCreated:*`、`s3:ObjectRemoved:*` 等通知并按对象前缀/后缀过滤。原生 `Records[]` 的 key 做 form URL 解码；EventBridge 的 key 按原值匹配。支持 S3 测试事件，不触发刷新。直连 S3 的 SNS 目标应为 Standard topic。
- AppConfig 为 application/environment/profile 配置 SNS extension association，启用 `ON_DEPLOYMENT_START`、`ON_DEPLOYMENT_COMPLETE`、`ON_DEPLOYMENT_ROLLED_BACK`。消息中的 `OnDeploymentStart`、`OnDeploymentComplete`、`OnDeploymentRolledBack` 触发检查，但不能绕过 Data API 轮询间隔。
- Parameter Store 建立 `source: aws.ssm`、`detail-type: Parameter Store Change` 的 EventBridge rule，将事件送至 SNS；路径按完整 `/` 段匹配，`/app` 不匹配 `/application`。该来源是 best effort，必须保留校准。
- 配置 SQS redrive policy、DLQ 和告警。运行时身份至少需要目标队列的 `sqs:ReceiveMessage`、`sqs:DeleteMessage`；加密资源还需对应 KMS 授权。数据读取身份分别需要 `appconfig:StartConfigurationSession` / `appconfig:GetLatestConfiguration`、`s3:GetObject`（固定版本时 `s3:GetObjectVersion`）、`ssm:GetParametersByPath`（解密时按需 `kms:Decrypt`）。

消费者每次接收一条，长轮询 20 秒；visibility 为刷新超时向上取整加 10 秒，刷新必须尊重 context。刷新失败不删除，确认失败和 `DeleteMessageBatch.Failed` 都会报告。SDK HTTP 超时必须覆盖长轮询；确认使用独立的 5 秒超时，剩余 5 秒余量留给传输开销。超时/重投由幂等 Reload 和周期校准收敛。

旧 `providers/s3/s3events.Watch` 只把事件异步交给 Manager，可能在实际 Reload 前删除消息，仍是尽力通知。可靠场景迁移到此同步回调，不要同时为同一队列启动旧 Watch 和新 Consumer；暂不迁移的应用也应添加周期 Reload。

参考：[SNS raw delivery](https://docs.aws.amazon.com/sns/latest/dg/sns-large-payload-raw-message-delivery.html)、[S3 消息格式](https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-content-structure.html)、[AppConfig SNS extension](https://docs.aws.amazon.com/appconfig/latest/userguide/working-with-appconfig-extensions-about-predefined-notification-sns.html)、[Parameter Store 事件](https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-paramstore-cwe.html)。

## Azure：Event Grid 经 Service Bus 消费

```go
import notify "github.com/fastabc/fastconf/integrations/notify/azservicebus"

consumer, err := notify.New(notify.Config{
    Source: notify.AppConfiguration, ResourceID: storeResourceID,
    Prefix: "myapp:", Label: "prod", RefreshTimeout: 30 * time.Second,
}, receiver) // *azservicebus.Receiver，必须用 PeekLock
// Blob 替换为 Source: notify.Blob、ResourceID: storageAccountResourceID、
// Container: "config"、Key: "prod/app.yaml"。
// 检查 err 后调用 consumer.Run(ctx, refresh)。
```

建立 Service Bus topic，为各接收域创建 subscription；Event Grid subscription 的 endpoint 指向 topic，delivery schema 明确选择 **EventGridSchema**，不是 CloudEvents。绑定 App Configuration 的 KeyValueModified/KeyValueDeleted，或 Storage 的 BlobCreated/BlobDeleted。设置资源与事件过滤，并授权 Event Grid 身份向 Service Bus 发送；运行时身份需要 Azure Service Bus Data Receiver，加载器分别需要 App Configuration Data Reader 或 Storage Blob Data Reader。SAS 用户也应仅授予所需读取/接收权限。

配置 subscription 的 `MaxDeliveryCount`、死信告警与 Event Grid 投递失败处理。`LockDuration` 应大于 `RefreshTimeout`，留出确认余量。消费者只接收一条，刷新 deadline 进一步限制在剩余锁时间前 1 秒，不自动续锁；成功 Complete，失败 Abandon，锁过期或确认失败允许重投。应用使用 `ReceiveAndDelete` 会失去此语义，消费者会拒绝没有锁期限的消息。

App Configuration 按 resource ID、key prefix、精确 label 筛选；空 label 表示无标签。Blob 按 storage account 和 `data.url` 解析出的 container、完整 key 筛选，跳过未提交的 HNS/SFTP 创建/写入事件。首版只支持 EventGridSchema 的对象或数组。参考：[Event Grid → Service Bus](https://learn.microsoft.com/en-us/azure/event-grid/handler-service-bus)、[App Configuration schema](https://learn.microsoft.com/en-us/azure/event-grid/event-schema-app-configuration)、[Blob schema](https://learn.microsoft.com/en-us/azure/event-grid/event-schema-blob-storage)。

## GCP：Pub/Sub v2 消费

```go
import notify "github.com/fastabc/fastconf/integrations/notify/pubsub"

subscriber := pubsubClient.Subscriber(subscriptionID) // cloud.google.com/go/pubsub/v2
subscriber.ReceiveSettings.MaxOutstandingMessages = 1
subscriber.ReceiveSettings.MaxOutstandingBytes = 1 << 20
subscriber.ReceiveSettings.MaxExtension = 2 * time.Minute
consumer, err := notify.New(notify.Config{
    Source: notify.GCS, Bucket: "config", Key: "prod/app.yaml",
    RefreshTimeout: 30 * time.Second,
}, subscriber)
// Secret Manager 替换为 Source: notify.SecretManager、
// Secret: "projects/my-project/secrets/my-config"（不含 /versions）。
// 检查 err 后调用 consumer.Run(ctx, refresh)。
```

先创建 topic 和每个接收域的 subscription。GCS 为 bucket 配置 Pub/Sub notification，包含 OBJECT_FINALIZE、OBJECT_DELETE、OBJECT_ARCHIVE、OBJECT_METADATA_UPDATE；授权 Storage service agent 发布。Secret Manager 在 secret 的 topics 中绑定 topic，授权 Secret Manager service agent 发布。应用需要对应 subscription 的 Pub/Sub Subscriber，读取权限分别为目标 bucket/object 的 Storage Object Viewer、目标 secret 的 Secret Manager Secret Accessor。运行时不需要 topic/subscription 管理权限。

消费依据服务定义的 attributes：GCS 的 `bucketId/objectId/eventType`，Secret Manager 的完整 `secretId/eventType`。Secret Manager 的 TOPIC_CONFIGURED 和 SECRET_ROTATE 不代表新版本可读，不触发刷新；事件 payload 不作为秘密正文。版本/alias 由 provider 重新访问。

应用在交给消费者前设置 SDK 的 outstanding messages/bytes 和 lease extension；FastConf 不修改借用的 subscriber。刷新超时应小于 `MaxExtension`，回调成功后 Ack，失败 Nack。配置 subscription 的 retry backoff、dead-letter topic、最大投递次数和相应 service agent 权限。订阅回调可以并发，`OnError` 需线程安全；每个 subscriber 同时只运行一个 Receive。参考：[GCS 通知](https://docs.cloud.google.com/storage/docs/pubsub-notifications)、[Secret Manager 通知](https://docs.cloud.google.com/secret-manager/docs/event-notifications)、[Pub/Sub v2](https://pkg.go.dev/cloud.google.com/go/pubsub/v2#Subscriber.Receive)。

`Ack()` / `Nack()` 仅把确认请求交给 SDK 异步发送，没有返回错误；本模块的 `OnError` 只报告解析、刷新和回调取消错误，`Run` 返回接收失败。应用应通过 SDK/服务端指标、重复投递和死信监控确认链路。若需要逐条确认结果，需要另行使用 SDK 的 `AckWithResult` / `NackWithResult` 并启用符合条件的 exactly-once subscription；当前消费者不提供这项保证。参考：[Pub/Sub exactly-once delivery](https://docs.cloud.google.com/pubsub/docs/exactly-once-delivery)。

## 诊断与本地验证边界

`appconfig`、`azappconfig`、`azblob`、`secretmanager`、`s3`、`gcs` 加载错误及三个通知模块的默认错误文本隐藏 token、SAS 和文档正文，保留 `errors.Is`/`errors.As`；主动解包并打印原始 SDK 错误仍可能泄密。使用错误类型/状态码分类告警，不记录完整消息和秘密内容。Secret Manager 加载不会自动标记所有字段为 secret，应用仍应配置敏感字段规则，覆盖 dump/diff。

[示例索引](../../../examples/README.md)中的程序均不需要真实凭据。本地 SDK 协议、过滤、失败重投、确认顺序和 Manager 集成测试不等于云端验收；实际 IAM、Event Grid schema、AppConfig extension、Pub/Sub attributes 及多实例广播仍应在隔离资源中验收。
