# Azure App Configuration JSON settings

From the repository root, with the Go version in
[go.mod](../../providers/azappconfig/go.mod):

```sh
GOPROXY=direct bash tools/modules.sh exec go -C providers/azappconfig run ../../examples/azappconfig/main.go
GOPROXY=direct bash tools/modules.sh exec go -C providers/azappconfig test -race ../../examples/azappconfig/main.go ../../examples/azappconfig/main_test.go
```

[main.go](main.go) uses the real Azure SDK with an in-memory HTTP transport and
synthetic connection-string key. There is no listener or cloud access. It selects
prefix `app:` and label `prod`, then compares default string-valued settings with
`azappconfig.WithJSONValues()`:

```text
default limits type=string
json settings: host=localhost rps=10 enabled=true
```

Colon-separated keys become nested fields. With the option enabled,
`application/json` settings (including media-type parameters) decode as JSON;
ordinary settings stay strings. JSON numbers retain precision before typed
loading through `fastconf.Load`. Key Vault references and feature-flag content
types still require separate handling and are rejected by this provider.

In production inject an application-configured SDK client. Empty label selects
unlabelled settings. See [loading](../../docs/cookbook/sources/cloud.md) and
[Event Grid/Service Bus](../../docs/cookbook/sources/cloud-notifications.md).
