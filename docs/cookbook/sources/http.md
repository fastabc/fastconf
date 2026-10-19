# HTTP configuration documents

`providers/http` ships with the root module. It loads a remote document during
assembly and optionally polls for changes. Use an explicit import alias to
avoid confusion with the standard `net/http` package:

```go
import httpprov "github.com/fastabc/fastconf/providers/http"

p, err := httpprov.New("remote", "https://config.example.com/app.yaml", nil,
    httpprov.WithHeader("Authorization", "Bearer "+token),
)
if err != nil {
    return err
}
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithProvider(p),
    fastconf.WithUnknownFields(fastconf.UnknownError),
)
if err != nil {
    return err
}
defer mgr.Close()
// Keep the manager alive for the service lifetime.
```

`Config`, `ctx` and `token` are application values. This fragment also imports
`github.com/fastabc/fastconf`. Providers override directory layers; `conf.d`
remains the default directory even when a provider is registered.

## Decoding and updates

A nil decoder selects a registered codec from the response `Content-Type`,
not the URL extension. For a server without a useful content type, obtain an
explicit decoder with `codec.For("yaml")`, check its error, and pass it to `New`.

Polling defaults to 30 seconds and starts with `New` independently of
`WithWatch`, which only enables file watching. `WithInterval(0)` disables
polling while preserving initial and manual loads. `Load` performs one fetch
without starting watchers.

Accepted ETags enable conditional requests; a 304 reuses the accepted document.
Body hashes also detect changes. A changed response triggers a full reload;
only a successful typed candidate publishes a new manager snapshot. Polling
transport errors are currently retried at the next interval without emitting a
`ProviderError`; errors from an actual reload reach `Errors()`.

## Limits and verification

The default client has a 10-second timeout and rejects redirects. Documents
are limited to 4 MiB; `WithMaxBodyBytes` changes the limit. A custom `WithClient`
must enforce its own timeout and credential/redirect policy. See
[provider timeouts](../runtime/provider-timeouts.md).

Run the local HTTP provider tests, which use test servers and clients:

```sh
go test ./providers/http
```
