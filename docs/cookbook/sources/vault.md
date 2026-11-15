# Vault KV-v2 recipe

```go
import (
    "os"
    "time"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/vault"
)

p, err := vault.New(
    os.Getenv("VAULT_ADDR"),
    "myapp/prod",
    os.Getenv("VAULT_TOKEN"),
    vault.WithMount("secret"),
    vault.WithInterval(30*time.Second),
)
if err != nil {
    return err
}
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(p),
    fastconf.WithWatch(fastconf.Watch{}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Provider watches start automatically; `WithWatch` here only enables file
watching for the `conf.d` layers.

Use `WithAuth` to supply an authentication implementation, such as
`AppRoleAuth`; it re-logs in before the issued TTL expires. Static tokens do not
renew. `WithInterval(0)` disables metadata polling; auth renewal is controlled
separately by `WithRenewBefore`.

`AppRoleAuth` accepts complete responses of at most 64 KiB. Read, size and decode
failures reject the login; displayed errors omit response values while
`errors.Is` / `errors.As` preserve the underlying cause.

The Vault provider polls the KV-v2 metadata endpoint and emits a reload
event whenever `current_version` changes. It does not currently expose a
resumable revision cursor; if you need disconnect resume semantics, wrap
Vault in a custom provider whose `Watch(ctx, from)` honors the revision.
Reading a KV secret does not mark its paths sensitive; configure `fc:"secret"`
or `WithSecretPaths` for diagnostic redaction. See [secrets](../processing/secrets.md).
Numeric values remain `json.Number` in provider snapshots and retain their exact
JSON literal when resolved as fields, including integers beyond float64 precision.

## Field references (v1.6.0)

`NewResolver(addr, token, opts...)` reuses the HTTP, mount, body-limit and
`WithAuth` options. Register it with
`fastconf.PrefixSecretResolver("vault", vault.ResolverPrefix, resolver.Resolve)`.
`ref+vault://app/db#password` reads one KV v2 scalar. Dynamic authentication retries
login once after a 403. Resolved values are automatically masked in diagnostics.
See [secret resolution](../processing/secrets.md) and the
[complete resolver/rotation example](../../../examples/secrets/README.md).
