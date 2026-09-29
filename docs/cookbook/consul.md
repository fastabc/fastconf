# Consul KV recipe

```go
import (
    "os"
    "time"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/consul"
)

p, err := consul.New(
    "http://127.0.0.1:8500",
    "myapp/",
    consul.WithToken(os.Getenv("CONSUL_HTTP_TOKEN")),
    consul.WithWait(10*time.Second),
)
if err != nil {
    return err
}
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithProvider(p),
)
```

Provider watches start automatically; `WithWatch` only controls the file
watcher for `WithDir` layers.

The provider uses Consul's blocking-query API (`?index=N`) so updates
trigger reloads after a changed query result; latency depends on Consul and the
network. Omit `consul.WithWait` to use
the default 5-minute blocking window.

In the default `ModeKV`, `myapp/` selects only keys below that directory, such
as `myapp/db/host`; it excludes siblings such as `myapp2/db/host` and
`myapp-staging/db/host`. Leading and trailing slashes are normalized. An empty
prefix selects the whole KV tree.

`ModeBlob` requires `WithCodec` and decodes the first non-empty, non-folder
document at the exact prefix key or below its directory. Its recursive query
keeps the exact key accessible; sibling prefixes are filtered before decoding.
Use a prefix containing a single document to avoid ambiguity.
