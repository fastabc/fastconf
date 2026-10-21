# Azure Blob Storage

From the repository root, using the optional module's declared Go version:

```sh
bash tools/modules.sh exec go -C providers/azblob run ../../examples/azblob/main.go
```

Expected output:

```text
map[server:map[port:9090]]
revision="example-v1" unchanged=true
```

[main.go](main.go) creates a real Azure Blob SDK client pointed at a local HTTP
endpoint. It loads YAML from `example/config.yaml`, then repeats the load with
`If-None-Match`. The endpoint returns 304 and the provider reuses its decoded
cache. No Azure account, credentials or external service is needed.

For Azure, replace the local client with your application's configured
`*azblob.Client` using token credentials, shared key or SAS, and select a container
and blob key. Pass the provider to `fastconf.WithProvider` when using
`fastconf.Load` or `fastconf.New`; call `Manager.Reload` for explicit refresh.
There is no automatic polling or Service Bus integration.

This provider was added after v1.1.0. The example uses `//go:build ignore` to
keep Azure SDK dependencies outside the root module; naming `main.go` runs it
through the [provider module](../../providers/azblob/go.mod). `make example`
runs the same temporary-workspace command shown above.

See [embedded files and cloud sources](../../docs/cookbook/sources/cloud.md).
