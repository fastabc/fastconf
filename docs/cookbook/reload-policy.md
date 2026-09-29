# Reload failure policy & one-shot overrides

FastConf's reload semantics are **failure-safe**: any pipeline stage error preserves the previous `*State[T]`, and `Get()` keeps returning the last good value. This is the right default for long-lived workers carrying in-flight requests.

For consumers that want to observe / react to failures, the framework exposes a streaming channel. For ad-hoc operator overrides, `Reload` accepts a one-shot override layer.

## `m.Errors()` — per-failure event stream

Every failed reload emits one `ReloadError` onto a buffered channel. The channel is closed when the Manager closes.

```go
import "github.com/fastabc/fastconf"

mgr, _ := fastconf.New[Cfg](ctx, fastconf.WithDir("conf.d"))
defer mgr.Close()

go func() {
    for re := range mgr.Errors() {
        slog.Error("reload failed", "reason", re.Reason, "err", re.Err)
    }
}()
```

`ReloadError` fields:

```go
type ReloadError struct {
    Err    error      // pipeline, cancellation, or subscriber error; inspect with errors.Is
    Reason string     // "manual" / "watcher" / "provider:vault" / "override" / ...
    When   time.Time  // wall-clock when the reload attempt completed
}
```

Capacity is 16 with **drop-on-full** semantics — if the consumer cannot keep up, the oldest pending error is dropped. The reload loop is never blocked by a slow consumer. Failure-safe state preservation is unaffected by drops.

### Consumer pattern: "abort after N consecutive failures"

Use one synchronous observer to count failures and reset on every successful
pipeline attempt, including unchanged configurations. `Errors()` alone cannot
report success, and `Subscribe` only runs after a commit.

```go
import "github.com/fastabc/fastconf/observe"

appCtx, stop := context.WithCancel(context.Background())
defer stop()
consecutive := 0
mgr, err := fastconf.New[Cfg](appCtx,
    fastconf.WithDir("conf.d"),
    fastconf.WithObserver(observe.Func(func(_ context.Context, e fastconf.Event) {
        result, ok := e.(fastconf.ReloadFinished)
        if !ok {
            return
        }
        if result.Err == nil {
            consecutive = 0
            return
        }
        consecutive++
        if consecutive >= 3 {
            slog.Error("3 consecutive reload failures", "last_err", result.Err)
            stop() // signal the owner; do not call mgr.Close inside the callback
        }
    })),
)
if err != nil {
    log.Fatal(err)
}
defer mgr.Close()
<-appCtx.Done()
```

Keep this observer local to one manager. Reload events run serially, so the
counter needs no lock. The owner closes the manager after receiving the stop
signal; canceling the initialization context alone does not stop its workers.

## `Reload(ctx, WithOverride(map))` — one-shot override

For "I just want to test this one override" without writing a file or wiring a provider:

```go
err := mgr.Reload(ctx,
    fastconf.WithOverride(map[string]any{
        "server": map[string]any{"addr": ":9090"},
    }),
)
```

Behaviour:

- Full reload pipeline runs with an extra in-memory layer at priority 9000, above all providers.
- The layer is **one-shot** — the next plain `mgr.Reload(ctx)` reverts to the natural source set.
- Values must be JSON-serializable. `Reload` **deep-copies** the map when applying the option; do not mutate it concurrently with the call. The original can be reused after `Reload` returns.
- Useful for integration tests and application-level operator overrides without editing source files.

Additional `ReloadOption`:

- `fastconf.WithReason(s string)` — overrides the default `"manual"` reason tag stamped onto audit / metric / log lines.

## Why no `mgr.Set(key, value)`?

A partial-mutation API would break:

- hash dedupe (the canonical hash is over the full `*T`),
- provenance (each leaf carries the layer that wrote it),
- subscriber fan-out (every reload must be atomic),
- audit (one cause per state change).

`WithOverride` is the sanctioned shape for "apply just this much change" — it produces a real reload with a real audit entry and reverts automatically on the next plain `Reload`.
