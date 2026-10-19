# Provider timeouts and cancellation

Reloads, plans and rollbacks share one writer. A slow provider delays the next
operation while readers continue using the current snapshot.

## Context and client limits

`Reload(ctx)` and `Plan(ctx)` pass cancellation to provider loads, generators,
secret resolvers and policies. Synchronous transforms and validators have no
context parameter; cancellation is checked at stage boundaries. A deadline
cannot interrupt a hook that ignores it. Shutdown cancels manager work but must
still wait for such hooks to return.

File/provider-triggered reloads use the manager's background context without a
per-operation deadline. The constructor context only bounds initialization.
Set network timeouts appropriate to the upstream protocol.

| Provider | Default client timeout | Watch behavior |
|---|---|---|
| `providers/http` | 10 seconds | Polls every 30 seconds by default |
| `providers/vault` | 10 seconds | Metadata polling; optional auth re-login |
| Vault `AppRoleAuth` with nil client | 10 seconds | Bounded login POST |
| `providers/consul` | No total timeout | Blocking query; default wait 5 minutes |

Built-in clients reject redirects. `WithClient` accepts an application-owned
HTTP `Doer`; it is not modified by the provider. The caller must enforce its
own timeout, redirect and credential policy. Consul's blocking request timeout
must exceed its configured wait window; short fixed timeouts cause repeated
reconnections.

## Bound a manual reload

This fragment imports `context`, `time` and `errors`. Handle the returned error
in the application's control path:

```go
reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
if err := mgr.Reload(reloadCtx); err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        return fmt.Errorf("reload exceeded its deadline: %w", err)
    }
    return err
}
```

Also import `fmt` for the wrapped error. Cooperative I/O stops at cancellation;
publication checks preserve the live snapshot when the operation is canceled.
See [reload failures](reload-policy.md) for the error consumer and
[runtime lifecycle](../../design/spec.md#reads-and-writes) for shutdown limits.

## Implementing a provider

Honor `ctx` in `Load` and optional `Watch`; use context-aware client calls and
close watcher channels when publishers stop. Bound retry loops and request
sizes. Choose client/transport timeouts for each request shape, rather than
assuming a reload deadline exists for background triggers.

Provider implementations and tests are colocated under
[`providers/http`](../../../providers/http), [`providers/vault`](../../../providers/vault)
and [`providers/consul`](../../../providers/consul). Use
`contracts/providertest` when testing a custom provider's lifecycle.
