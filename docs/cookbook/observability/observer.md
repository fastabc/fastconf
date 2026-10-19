# Observer lifecycle events

`WithObserver` receives reload results, stage timings, provider notifications
and commit events. Use it for audit and telemetry; use
[Subscribe](../runtime/subscribe.md) to compare selected configuration values.

## Events

| Event | When |
|---|---|
| `ReloadStarted` | A reload begins |
| `ReloadFinished` | A reload ends; `Err == nil` includes unchanged values |
| `StageFinished` | A stage completes, including pipeline stages during Plan |
| `ProviderError` | A provider `Watch` call fails or reports a resume gap |
| `EventDropped` | A provider trigger overflows the manager's reload queue |
| `Committed` | A snapshot publishes, including initial load and rollback |

Switch on concrete event types and ignore unneeded types. `Committed` carries
previous/next generations, cause, layer count and a lazy redacted `Diff()`.
Initial publication has `Prev == 0` and no previous snapshot to diff.
Provider-internal retry errors are not necessarily exposed as `ProviderError`.

## Delivery and composition

Reload, stage and commit events run synchronously on the loading/writer
goroutine, in registration order. Provider/drop events may arrive concurrently;
observers must be safe for that use. Do not wait for the same manager's reload,
Plan, rollback or shutdown from a synchronous callback.

`WithObserverTimeout` cancels the callback context after 2 seconds by default;
a negative value disables the deadline. It cannot interrupt code that ignores
cancellation. Slow callbacks delay the writer.

| Helper in `observe` | Purpose |
|---|---|
| `Func(fn)` | Adapt a function |
| `Multi(obs...)` | Fan out in order |
| `Async(obs, capacity)` | Bounded queue with one consumer goroutine |
| `JSONLines(w)` | One audit JSON line per commit, not per failure |
| `Metrics(sink)` | Translate lifecycle events to metric calls |

## Asynchronous external delivery

This fragment imports `context`, `log`, `time`, `fastconf` and `fastconf/observe`
under the repository's module path. `sendAudit` is an application function
accepting `(context.Context, fastconf.Committed)` and returning an error:

```go
audit := observe.Async(observe.Func(func(ctx context.Context, e fastconf.Event) {
    c, ok := e.(fastconf.Committed)
    if !ok || c.Prev == 0 {
        return
    }
    ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    if err := sendAudit(ctx, c); err != nil {
        log.Printf("audit delivery failed: %v", err)
    }
}), 64)
defer audit.Close()

mgr, err := fastconf.New[Config](ctx,
    fastconf.WithObserver(observe.Multi(observe.JSONLines(w), audit)),
)
if err != nil {
    return err
}
defer mgr.Close() // Runs before audit.Close, so queued events can drain.
```

`Config`, `ctx` and writer `w` are application values. Async delivery uses
`context.Background()` for its target; the manager's callback deadline and
shutdown cancellation are not propagated. Set a timeout inside the target and
ensure the transport honors it. `Close` waits for queued callbacks to finish.

A full async queue drops new events and increments `Dropped()`; there is no
retry or final-state delivery guarantee. This is separate from provider queue
overflow, which retains one compensating reload in the current checkout.
See the [runtime contract](../../design/spec.md#reads-and-writes) for progress
conditions and [resource-update](../../../examples/resource-update/README.md)
for bounded business resource changes.

`Committed` does not fire for rejected or unchanged candidates. Handle
`ReloadFinished` for success/failure counts and `Errors()` for failures.
See [metrics and tracing](observability.md) for instrumentation.
