# AWS AppConfig

From the repository root, with the Go version in
[go.mod](../../providers/appconfig/go.mod):

```sh
bash tools/modules.sh exec go -C providers/appconfig run ../../examples/appconfig/main.go
```

[main.go](main.go) uses the real AWS SDK with a local HTTP endpoint and anonymous
credentials. It starts a Data API session, loads a JSON document, and reads the
cached document before the next allowed poll. Expected output:

```text
map[server:map[host:localhost]]
cached before next poll: true
```

The endpoint and tokens are local fixtures. In production inject your configured
AppConfig Data client; keep periodic reloads even with deployment notifications.
See [loading](../../docs/cookbook/sources/cloud.md) and
[notifications](../../docs/cookbook/sources/cloud-notifications.md).
