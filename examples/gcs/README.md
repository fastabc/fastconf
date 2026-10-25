# Google Cloud Storage

From the repository root, using the optional module's declared Go version:

```sh
bash tools/modules.sh exec go -C providers/gcs run ../../examples/gcs/main.go
```

Expected output:

```text
map[server:map[port:9090]]
revision=1 unchanged=true
```

[main.go](main.go) uses a real GCS SDK client with a local JSON API endpoint.
The provider loads YAML from `example/config.yaml`, pins the read to generation
`1`, then checks metadata on a second load and reuses the decoded document.
No GCP account, credentials or external service is needed.

For GCP, supply your application's authenticated `*storage.Client`, bucket and
object key. The application owns the client and closes it when finished.
Pass the provider to `fastconf.WithProvider` when using `fastconf.Load` or
`fastconf.New`; call `Manager.Reload` for explicit refresh. There is no automatic
polling or Pub/Sub integration.

This provider was added in v1.2.0. The example uses `//go:build ignore` to
keep Google SDK dependencies outside the root module; naming `main.go` runs it
through the [provider module](../../providers/gcs/go.mod). `make example` runs
the same temporary-workspace command shown above.

See [embedded files and cloud sources](../../docs/cookbook/sources/cloud.md).
