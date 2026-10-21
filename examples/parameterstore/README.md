# AWS Parameter Store

From the repository root, using the optional module's declared Go version:

```sh
bash tools/modules.sh exec go -C providers/parameterstore run ../../examples/parameterstore/main.go
```

Expected output:

```text
map[server:map[host:localhost port:9090]]
```

[main.go](main.go) constructs a real AWS SSM client pointed at a local HTTP
endpoint. The endpoint serves two pages of parameters under `/example`.
The provider combines them into a nested configuration tree, with parameter
values preserved as strings (including `"9090"`). `Decrypt: true` demonstrates
requesting SecureString decryption; the local endpoint does not contact KMS.
No AWS account, credentials or external service is needed.

For AWS, replace the local client setup with `ssm.NewFromConfig` using your
application's configured `aws.Config`. Supply the desired path and decryption
permissions. Pass the resulting provider to `fastconf.WithProvider` when using
`fastconf.Load` or `fastconf.New`; call `Manager.Reload` for explicit refresh.

This provider was added after v1.1.0. The example uses `//go:build ignore` to
keep AWS dependencies outside the root module; naming `main.go` runs it through
the [provider module](../../providers/parameterstore/go.mod). `make example`
runs the same temporary-workspace command shown above.

See [embedded files and cloud sources](../../docs/cookbook/sources/cloud.md).
