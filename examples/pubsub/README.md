# GCP Pub/Sub v2

From the repository root, with the Go version in
[go.mod](../../integrations/notify/pubsub/go.mod):

```sh
bash tools/modules.sh exec go -C integrations/notify/pubsub run ../../../examples/pubsub/main.go
```

[main.go](main.go) connects the real Pub/Sub v2 SDK to a local gRPC server with
synthetic GCS attributes. No external service or credentials are needed.
Expected output:

```text
refresh successful
ack after refresh
```

Production passes `mgr.Reload` as the callback and configures subscriber flow
control, lease extension, retries and independent subscriptions. See the
[cloud setup guide](../../docs/cookbook/sources/cloud-notifications.md) and
[periodic reconciliation](../cloud-reload/README.md).
