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
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithProvider(p),
    fastconf.WithWatch(true),
)
```

The provider uses Consul's blocking-query API (`?index=N`) so updates
are pushed within ~100ms of the KV write. Omit `consul.WithWait` to use
the default 5-minute blocking window.
