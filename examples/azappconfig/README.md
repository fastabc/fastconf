# Azure App Configuration

From the repository root, with the Go version in
[go.mod](../../providers/azappconfig/go.mod):

```sh
bash tools/modules.sh exec go -C providers/azappconfig run ../../examples/azappconfig/main.go
```

[main.go](main.go) uses the real Azure SDK against a local HTTP endpoint with a
synthetic connection-string key. Selecting prefix `app:` and label `prod`
produces `map[server:map[host:localhost]]`. Values remain strings; `:` creates
nested keys. The signing key has no cloud access.

In production inject an application-configured SDK client. Empty label means
unlabelled settings. See [loading](../../docs/cookbook/sources/cloud.md) and
[Event Grid/Service Bus](../../docs/cookbook/sources/cloud-notifications.md).
