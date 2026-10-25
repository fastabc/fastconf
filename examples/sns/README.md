# SNS through SQS

From the repository root, with the Go version in
[go.mod](../../integrations/notify/sns/go.mod):

```sh
bash tools/modules.sh exec go -C integrations/notify/sns run ../../../examples/sns/main.go
```

[main.go](main.go) uses a synthetic SNS envelope containing an S3 event, a local
SQS API double and a real FastConf Manager. Its synchronous reload completes
before message deletion. Expected output:

```text
ack after reload
configuration: new
```

Use an application-configured `*sqs.Client` in production. This demo does not
validate AWS permissions or delivery. Keep [periodic reconciliation](../cloud-reload/README.md)
and follow the [cloud setup guide](../../docs/cookbook/sources/cloud-notifications.md).
