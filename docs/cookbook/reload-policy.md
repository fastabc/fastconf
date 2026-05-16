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
    Err    error      // the wrapped reload error (errors.Is(., ErrFastConf) is true)
    Reason string     // "manual" / "watcher" / "provider:vault" / "override" / ...
    When   time.Time  // wall-clock when the reload attempt completed
}
```

Capacity is 16 with **drop-on-full** semantics — if the consumer cannot keep up, the oldest pending error is dropped. The reload loop is never blocked by a slow consumer. Failure-safe state preservation is unaffected by drops.

### Consumer pattern: "abort after N consecutive failures"

```go
go func() {
    var consec int
    for re := range mgr.Errors() {
        consec++
        if consec >= 3 {
            slog.Error("3 consecutive reload failures, exiting", "last_err", re.Err)
            os.Exit(1)
        }
    }
}()

// A successful reload should reset the streak; cleanest pattern is to also
// subscribe to commits and reset the counter on success. Since the channel
// only fires on failure, you can use a Subscribe callback to reset:
fastconf.Subscribe(mgr, func(c *Cfg) *Cfg { return c }, func(_, _ *Cfg) {
    // any successful commit -- reset consec via a mutex or atomic
})
```

## `Reload(ctx, WithSourceOverride(map))` — one-shot override

For "I just want to test this one override" without writing a file or wiring a provider:

```go
err := mgr.Reload(ctx,
    fastconf.WithSourceOverride(map[string]any{
        "server": map[string]any{"addr": ":9090"},
    }),
)
```

Behaviour:

- Full reload pipeline runs with an extra in-memory layer at priority `PriorityCLI + 1000`.
- The layer is **one-shot** — the next plain `mgr.Reload(ctx)` reverts to the natural source set.
- The `override` map is consumed as input and is **not deep-copied**; do not mutate it after calling.
- Useful for `fastconfctl rehearse` workflows, integration tests, and operator overrides during incident response.

Additional `ReloadOption`:

- `fastconf.WithReloadReason(s string)` — overrides the default `"manual"` reason tag stamped onto audit / metric / log lines.

## Why no `mgr.Set(key, value)`?

A partial-mutation API would break:

- hash dedupe (the canonical hash is over the full `*T`),
- provenance (each leaf carries the layer that wrote it),
- subscriber fan-out (every reload must be atomic),
- audit (one cause per state change).

`WithSourceOverride` is the sanctioned shape for "apply just this much change" — it produces a real reload with a real audit entry and reverts automatically on the next plain `Reload`.
