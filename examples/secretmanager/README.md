# GCP Secret Manager

From the repository root, with the Go version in
[go.mod](../../providers/secretmanager/go.mod):

```sh
GOPROXY=direct bash tools/modules.sh exec go -C providers/secretmanager run ../../examples/secretmanager/main.go
```

[main.go](main.go) uses the real REST SDK against a local HTTP endpoint without
authentication. The fixture contains a document and its CRC32C checksum.
`latest` resolves to version 1. The program prints the version and masked diagnostics:

```text
loaded version: projects/local/secrets/config/versions/1
runtime-ready=true diagnostics=map[host:***REDACTED***]
```

Production applications supply their own authenticated client and close it after
consumers stop. Configure secret field rules before exposing snapshots or diffs.
See [loading](../../docs/cookbook/sources/cloud.md) and
[Pub/Sub notifications](../../docs/cookbook/sources/cloud-notifications.md).

The program completes the FastConf pipeline with typed loading, field constraints
and `UnknownError`. Explicit providers use an empty discovery filesystem to keep
unrelated local files out of the load.
