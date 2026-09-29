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
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(p),
    fastconf.WithWatch(fastconf.Watch{}),
)
```

Provider watches start automatically; `WithWatch` here only enables file
watching for the `conf.d` layers.

The Vault provider polls the KV-v2 metadata endpoint and emits a reload
event whenever `current_version` changes. It does not currently expose a
resumable revision cursor; if you need disconnect resume semantics, wrap
Vault in a custom provider whose `Watch(ctx, from)` honors the revision.
