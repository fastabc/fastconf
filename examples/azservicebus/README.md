# Event Grid through Azure Service Bus

From the repository root, with the Go version in
[go.mod](../../integrations/notify/azservicebus/go.mod):

```sh
bash tools/modules.sh exec go -C integrations/notify/azservicebus run ../../../examples/azservicebus/main.go
```

[main.go](main.go) uses an SDK-compatible local receiver with an EventGridSchema
App Configuration event and a message lock. Expected output:

```text
refresh successful
complete after refresh
```

Production supplies a `*azservicebus.Receiver` in PeekLock mode and passes
`mgr.Reload` as the callback. This demonstrates settlement order, not cloud
delivery. Configure lock duration, dead-letter handling and separate subscriptions
using the [setup guide](../../docs/cookbook/sources/cloud-notifications.md), and
keep [periodic reconciliation](../cloud-reload/README.md).
