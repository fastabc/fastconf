# Cloud notifications and reliable refresh

Cloud loaders read complete configurations. Notification consumers call a
synchronous refresh function and acknowledge matching events only after it
succeeds. Return `mgr.Reload(work)` errors to the consumer; starting a goroutine
and immediately returning nil can acknowledge an update before it loads.

| Provider | Loading module | Notification module | Delivery path |
|---|---|---|---|
| AWS AppConfig | `providers/appconfig` | `integrations/notify/sns` | deployment SNS extension → SNS → SQS |
| AWS S3 | `providers/s3` | `integrations/notify/sns` | S3 → SNS → SQS, or EventBridge → SQS |
| AWS Parameter Store | `providers/parameterstore` | `integrations/notify/sns` | EventBridge → SNS → SQS |
| Azure App Configuration | `providers/azappconfig` | `integrations/notify/azservicebus` | Event Grid → Service Bus topic/subscription |
| Azure Blob | `providers/azblob` | `integrations/notify/azservicebus` | Event Grid → Service Bus topic/subscription |
| GCP Secret Manager | `providers/secretmanager` | `integrations/notify/pubsub` | secret notification topic → Pub/Sub subscription |
| GCS | `providers/gcs` | `integrations/notify/pubsub` | bucket notification → Pub/Sub subscription |

See [cloud sources](cloud.md) for loading, value types and client ownership.
The application configures SDK authentication and closes clients after workers
stop. FastConf creates no cloud resources. For AWS, `s3.NewWithClient` accepts an
SDK client with the application's credential chain.

## Managed lifecycle and recovery

Since v1.6.0, register `contracts.Notifier` implementations with the manager:

```go
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithProvider(remote),
    fastconf.WithNotifier(consumer),
    fastconf.WithResync(5*time.Minute),
)
if err != nil { return err }
defer mgr.Close()
```

The manager supervises `Run`. Early returns, including nil and panics, emit a
`ProviderError`, log a warning and restart with exponential backoff and jitter
from 250 ms to 30 seconds. Running for 30 seconds resets the backoff.
Periodic reconciliation adds up to 20% jitter. `Load` starts no workers and
ignores notifier/resync options. Pre-create queues/subscriptions and permissions
to cover the initialization window.

`Pause` skips periodic reconciliation. Notification refresh returns
`fastconf.ErrPaused`, so the consumer retains or negatively acknowledges the
message. `Resume` permits redelivery; it does not replay lost file/provider events.
Explicitly reload or rely on resync to recover those events. Pause does not drain
already queued work. `Close`/`Shutdown` cancel and join managed workers before
borrowed clients are closed; notifiers must respond to cancellation.
The [cloud-reload example](../../../examples/cloud-reload/README.md) demonstrates
restart, recovery from a deliberately missed notification, paused delivery and
successful redelivery.

Applications may instead supervise `consumer.Run(ctx, func(work context.Context)
error { return mgr.Reload(work) })` themselves. They then own restart backoff,
periodic reconciliation and shutdown ordering. `Run` reports receive failures;
`Config.OnError` reports parsing and refresh failures. Persistent authentication
failures need application alerting.

Each independent manager needs its own broadcast receiving domain: an SQS queue,
Service Bus subscription or Pub/Sub subscription. Replicas sharing one domain
compete for messages and may need to wait for resync. Use dedicated domains;
known unrelated events are acknowledged, while malformed events are retried or
dead-lettered. Filter cloud events as precisely as possible.

Events mean “read the current source again.” Duplicates and out-of-order delivery
do not directly install old values. Reloads skip publication when both the typed
hash and resolved-secret paths are unchanged; changes to those paths publish a
metadata commit without invoking value subscribers. Invalid configurations and
read/validation failures preserve the last valid snapshot.
ACK means one reload succeeded, without transactional consistency between an
event and a remote read. Visibility delays, gradual deployments and lost events
still require periodic reconciliation.

## AWS: SNS through SQS

```go
import notify "github.com/fastabc/fastconf/integrations/notify/sns"

consumer, err := notify.New(notify.Config{
    QueueURL: queueURL, TopicARN: topicARN,
    Source: notify.S3, Bucket: "config", Key: "prod/app.yaml",
    RefreshTimeout: 30 * time.Second,
}, sqsClient)
if err != nil { return err }
// Register consumer with WithNotifier, or supervise Run with a Reload callback.
```

Other selectors use `Source: notify.AppConfig` with `Application`, `Environment`
and `Profile`, or `Source: notify.ParameterStore` with `Path: "/myapp/prod"`.
See the [SNS example](../../../examples/sns/README.md) for actual reload-before-ACK.

Create a Standard SNS topic and one subscribed SQS queue per receiving domain.
Restrict SNS publishing and queue `sqs:SendMessage` policies by source topic ARN.
Both raw delivery and SNS envelopes work; `TopicARN` validates envelopes only,
so raw delivery relies on queue policies for source authenticity.

For S3, configure object-created/removed notifications with prefix/suffix filters.
Native `Records[]` keys use form URL decoding; EventBridge keys match literally.
S3 test events are accepted without refresh. Direct S3 SNS targets use Standard
topics. For AppConfig, associate the SNS extension with the application,
environment and profile for deployment start, complete and rollback events.
These events cannot bypass Data API poll intervals. For Parameter Store, route
`source: aws.ssm`, `detail-type: Parameter Store Change` EventBridge events to SNS.
Path matching respects complete segments: `/app` does not match `/application`.
This source is best effort; keep resync enabled.

Configure redrive policies, a DLQ and alerts. Runtime permissions include
`sqs:ReceiveMessage` and `sqs:DeleteMessage`, plus KMS permissions as needed.
Loading needs the appropriate `s3:GetObject`/`s3:GetObjectVersion`,
`ssm:GetParametersByPath`/`kms:Decrypt`, or
`appconfig:StartConfigurationSession`/`appconfig:GetLatestConfiguration` permissions.

The consumer receives one message with a 20-second long poll. Visibility is the
rounded-up refresh timeout plus 10 seconds. Refresh must honor context. Failed
refreshes are not deleted; settlement failures, including batch `Failed` entries,
are reported. SDK HTTP timeouts must cover long polling. Settlement has a separate
5-second timeout, leaving another 5 seconds for transport overhead.

`providers/s3/s3events` has been deprecated since v1.3.0 and remains until v2.
Its asynchronous Watch can delete events before a reload completes. Retain the
S3 loader and migrate notification handling to this synchronous SNS consumer.
Do not run both consumers against the same queue; retain periodic reloads during
migration.

## Azure: Event Grid through Service Bus

```go
import notify "github.com/fastabc/fastconf/integrations/notify/azservicebus"

consumer, err := notify.New(notify.Config{
    Source: notify.AppConfiguration, ResourceID: storeResourceID,
    Prefix: "myapp:", Label: "prod", RefreshTimeout: 30 * time.Second,
}, receiver) // An SDK receiver opened in PeekLock mode.
if err != nil { return err }
```

For Blob, select `notify.Blob`, the storage account `ResourceID`, `Container`
and full `Key`. See the [Service Bus example](../../../examples/azservicebus/README.md).
Create a Service Bus topic and a subscription per receiving domain. Point Event
Grid at that topic and explicitly select **EventGridSchema**. CloudEvents is
unsupported. Select App Configuration KeyValueModified/KeyValueDeleted or Blob
BlobCreated/BlobDeleted events, with resource/event filters.

Authorize Event Grid to send and the application to receive (Azure Service Bus
Data Receiver). Loading needs App Configuration Data Reader or Storage Blob Data
Reader. SAS permissions should likewise cover only required reads/receives.
Configure `MaxDeliveryCount`, dead-letter alerts and Event Grid delivery-failure
handling. Set `LockDuration` longer than `RefreshTimeout`, leaving settlement time.

One message is received at a time. Refresh deadlines stop at least one second
before lock expiry; locks are not automatically renewed. Success completes;
failure abandons. Expired locks or settlement errors permit redelivery.
ReceiveAndDelete loses this behavior; messages without a lock deadline are rejected.

App Configuration filters resource ID, key prefix and exact label; an empty label
means unlabeled. Blob filters storage account, container and full key parsed from
`data.url`, skipping uncommitted HNS/SFTP create/write events. EventGridSchema
objects and arrays are supported.

## GCP: Pub/Sub v2

```go
import notify "github.com/fastabc/fastconf/integrations/notify/pubsub"

subscriber := pubsubClient.Subscriber(subscriptionID)
subscriber.ReceiveSettings.MaxOutstandingMessages = 1
subscriber.ReceiveSettings.MaxOutstandingBytes = 1 << 20
subscriber.ReceiveSettings.MaxExtension = 2 * time.Minute
consumer, err := notify.New(notify.Config{
    Source: notify.GCS, Bucket: "config", Key: "prod/app.yaml",
    RefreshTimeout: 30 * time.Second,
}, subscriber)
if err != nil { return err }
```

For Secret Manager use `Source: notify.SecretManager` and a full `Secret` resource
name without `/versions`. The [Pub/Sub example](../../../examples/pubsub/README.md)
uses the real v2 SDK with a local gRPC broker and performs a real manager reload.

Create a topic and one subscription per receiving domain. GCS bucket notifications
include OBJECT_FINALIZE, OBJECT_DELETE, OBJECT_ARCHIVE and OBJECT_METADATA_UPDATE;
authorize the Storage service agent to publish. For Secret Manager, attach the
topic to the secret and authorize its service agent. Applications need Pub/Sub
Subscriber on their subscription and Storage Object Viewer or Secret Manager
Secret Accessor on the loaded resource; runtime topic administration is unnecessary.

Filtering uses service attributes: GCS `bucketId/objectId/eventType`, or Secret
Manager's full `secretId/eventType`. TOPIC_CONFIGURED and SECRET_ROTATE do not
indicate a readable new version and do not trigger refresh. Event payloads are
never used as secret data; the provider reads the configured version/alias.

Configure SDK outstanding messages/bytes and lease extension before handing over
the subscriber; FastConf does not modify it. Refresh timeout should be shorter
than `MaxExtension`. Successful callbacks Ack; failures Nack. Configure retry
backoff, dead-letter topics, delivery counts and service-agent permissions.
Callbacks may run concurrently, so `OnError` must be thread-safe. Run only one
Receive per subscriber.

`Ack()`/`Nack()` enqueue asynchronous SDK requests without returning settlement
errors. `OnError` reports parsing, refresh and callback cancellation errors;
`Run` reports receive failures. Monitor SDK/service metrics, duplicates and dead
letters. Per-message confirmation would require AckWithResult/NackWithResult
and an eligible exactly-once subscription; this consumer provides no such guarantee.

## Diagnostics and local verification

Cloud loader and notification errors mask tokens, SAS and document bodies while
preserving `errors.Is`/`errors.As`. Unwrapping and printing raw SDK errors can
still reveal sensitive data. Classify errors by type/status rather than logging
secrets. Secret Manager documents still require explicit secret tags/paths.

The [examples](../../../examples/README.md) require no real credentials. Local
SDK protocol, filtering, redelivery and settlement checks do not validate cloud
IAM, Event Grid schemas, AppConfig extensions or replica broadcast deployment.
Validate those with isolated cloud resources when deploying.
