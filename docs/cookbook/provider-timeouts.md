# Provider HTTP timeouts & cancellation

> Audience: anyone building or operating a remote-KV Provider (Vault,
> Consul, HTTP, your own). Pairs with [`reload-policy`](reload-policy.md).

FastConf's reload pipeline is **single-writer**: every reload — whether it
came from `fsnotify`, a Provider event, or `Manager.Reload(ctx)` — runs
serially. A slow Provider therefore directly translates to "the manager
is stuck", which in turn delays every Subscribe callback and every other
queued reload.

This page documents the timeout / cancellation model FastConf relies on
and the contract the built-in Providers honour.

## The model in one sentence

**Per-call lifetime is governed by `ctx`.** Set a `Reload(ctx)`
deadline and the cancellation flows into every `Provider.Load(ctx)`,
`SecretResolver` calls running inside that pipeline. Transformers are
synchronous callbacks without a context parameter; stage boundaries still
check cancellation before continuing.

This means HTTP-client `Timeout` is a *safety net*, not the primary knob.
You set it conservatively so a rogue Provider can't pin a goroutine
forever; you set the meaningful deadlines on the call ctx.

## Per-provider defaults

| Provider | Default `http.Client.Timeout` | Why |
|---|---|---|
| `providers/vault` (`vault.New`) | **10s** | Vault calls are short-lived `GET /v1/kv/...` requests; Watch polls every `Interval` (default 30s) and does not hold connections. A 10s wall-clock cap is a reasonable safety net. |
| `providers/vault` (`vault.AppRoleAuth` fallback) | **10s** | Same shape — single POST to `/v1/auth/approle/login`. |
| `providers/consul` (`consul.New`) | **none** (zero) | Consul uses blocking queries: each `fetch(ctx, index)` sets `?index=X&wait=5m`, asking Consul to hold the connection open until the next change or the wait window elapses. A 10s `Timeout` would tear the connection down before the server responds. Lifetime is therefore governed entirely by `ctx`. |
| `providers/http` (`httpprovider.New`) | **10s** | Single-shot `GET <url>`. |

## Customising the client

All three built-in providers expose `WithClient(Doer)` so you can plug in:

- A custom `*http.Client` with TLS / transport tuning
- An instrumented `Doer` that emits traces / metrics per request
- A test double in unit tests

```go
import (
    "net/http"
    "time"

    "github.com/fastabc/fastconf"
    consulprov "github.com/fastabc/fastconf/providers/consul"
)

// Consul: tune transport, NOT response-Timeout. Lifetime stays ctx-driven.
client := &http.Client{
    Transport: &http.Transport{
        TLSHandshakeTimeout:    5 * time.Second,
        ResponseHeaderTimeout: 150 * time.Second, // longer than the 2-minute wait below
        MaxIdleConnsPerHost:    2,
        IdleConnTimeout:        2 * time.Minute,
    },
}
cp, _ := consulprov.New("http://consul.svc:8500", "config/myapp",
    consulprov.WithClient(client),
    consulprov.WithWait(2*time.Minute), // override blocking-query wait
)

mgr, _ := fastconf.New[Cfg](ctx, fastconf.WithProvider(cp))
```

For Vault / HTTP, setting `Timeout` is fine — they don't block:

```go
import vaultprov "github.com/fastabc/fastconf/providers/vault"

vp, _ := vaultprov.New("https://vault.svc", "myapp", token,
    vaultprov.WithMount("kv"),
    vaultprov.WithClient(&http.Client{Timeout: 5 * time.Second}),
)
```

## Reload-side patterns

### CI / one-shot

For tools that call `Reload(ctx)` once and care about wall-clock budget,
attach a hard deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := mgr.Reload(ctx); err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        log.Fatal("reload exceeded 30s budget — investigate slow provider")
    }
    log.Fatal(err)
}
```

Cooperative I/O returns when the deadline trips; synchronous hooks that ignore
cancellation must return before the writer can finish. Cancellation checks at
stage boundaries prevent publication, preserving the live state.

### Long-running service

Manual reloads can use a per-call deadline. File/provider triggers use the
manager's background context, which is canceled at shutdown, without a per-call
deadline. Bound network operations with client/transport timeouts sized for the
upstream API. Shutdown cannot interrupt a provider that ignores cancellation.

### Errors() consumer

Cancellation remains recognizable through `errors.Is`, including through
provider/framework wrappers and errors delivered on `m.Errors()`:

```go
for re := range mgr.Errors() {
    switch {
    case errors.Is(re.Err, context.DeadlineExceeded):
        metrics.IncSlowProvider(re.Reason)
    case errors.Is(re.Err, context.Canceled):
        // Operator cancelled — usually expected; downgrade severity.
    default:
        log.Error("reload failed", "reason", re.Reason, "err", re.Err)
    }
}
```

## Writing your own Provider

When implementing `contracts.Provider`, the cancellation contract is:

1. **`Load(ctx)` MUST honour `ctx.Done()`** — return `ctx.Err()` promptly.
   If you call out to a custom client, use `http.NewRequestWithContext`
   (or the equivalent), never the deprecated context-less constructor.
2. **If you implement `contracts.Watcher`, `Watch(ctx, from)` must honor
   `ctx.Done()`** and should close its channel on cancellation. Load-only providers
   need only Name and Load; they do not need an empty Watch method.
3. **Do not use `http.DefaultClient`.** Build your own (`&http.Client{...}`)
   so you can be torn down without affecting unrelated callers.
4. **Pick a Timeout that matches your call shape.** Short single-shot
   requests → fixed `Timeout`. Blocking queries → no `Timeout`, rely on
   ctx + `Transport.ResponseHeaderTimeout`.

The unit-test fixture in `reload_test.go` (`blockingProvider` /
`toggleProvider`) is a useful template if you want to verify your own
Provider's cancellation behaviour end-to-end.
