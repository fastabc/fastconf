# Typed configuration from S3

Run from the repository root using the provider module's Go version:

```sh
GOPROXY=direct bash tools/modules.sh exec go -C providers/s3 run ../../examples/s3/main.go
```

Expected output:

```text
map[server:map[port:9090]]
typed port=9090
```

[main.go](main.go) uses the real AWS SDK with an anonymous local S3-compatible
HTTP endpoint. `NewWithClient` selects the bucket/key separately from client
construction; `WithMaxBodyBytes(4<<20)` bounds document size. The example first
inspects the decoded provider map, then passes the provider through
`fastconf.Load[Config]` with field validation and `UnknownError`. A bounded
context limits I/O; an empty discovery FS isolates this explicit provider.

Production applications inject a client with refreshing workload credentials,
choose the real object and configure transport/retry behavior. Use `New` with
`WithNotifier` and `WithResync` for live refresh. No cloud account is needed here.
See [cloud sources](../../docs/cookbook/sources/cloud.md) and
[notifications](../../docs/cookbook/sources/cloud-notifications.md).
