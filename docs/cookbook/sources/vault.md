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

The Vault provider polls the KV-v2 metadata endpoint and emits a reload
event whenever `current_version` changes. It does not currently expose a
resumable revision cursor; if you need disconnect resume semantics, wrap
Vault in a custom provider whose `Watch(ctx, from)` honors the revision.
Reading a KV secret does not mark its paths sensitive; configure `fc:"secret"`
or `WithSecretPaths` for diagnostic redaction. See [secrets](../processing/secrets.md).

## 字段引用 (v1.6.0)

`NewResolver(addr, token, opts...)` 复用 HTTP、mount、body limit 和 `WithAuth` 选项。
用 `fastconf.PrefixSecretResolver("vault", vault.ResolverPrefix, resolver.Resolve)`
注册后，`ref+vault://app/db#password` 读取 KV v2 的一个标量；动态 token 在 403 后重登一次。
解析值自动脱敏，详见[密钥解析](../processing/secrets.md)。
