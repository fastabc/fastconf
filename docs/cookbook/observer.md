# Observer

`WithObserver` is the single hook for everything that happens around a
reload: metrics, audit logs and change notifications (Slack bot, PagerDuty,
GitHub PR comment, …) are all observers.

## Events

```go
type Observer interface {
    Observe(ctx context.Context, e fastconf.Event)
}
```

| Event | When |
|---|---|
| `ReloadStarted{Reason}` | A reload begins |
| `ReloadFinished{Reason, Dur, Err}` | A reload ends; `Err` is nil on success, including no-op reloads |
| `StageFinished{Stage, Dur, Err}` | One pipeline stage (assemble, merge, …, commit); `Plan` dry-runs report theirs too |
| `ProviderError{Provider, Err}` | A provider `Watch` failed or could not resume |
| `EventDropped{Source}` | A provider change event was dropped because the reload queue was full |
| `Committed{Prev, Next, Cause, Layers, Diff}` | A new snapshot was published (including the initial load and rollbacks) |

Switch on the concrete type and ignore the rest; new event types may be added.

## Delivery

Reload, stage and `Committed` events are delivered synchronously on the
reload goroutine, in registration order. Each call receives a context
canceled at `WithObserverTimeout` (default `DefaultObserverTimeout`, 2s;
negative disables) or on shutdown. A slow observer therefore delays the next
reload — wrap anything that does network I/O with `observe.Async`:

```go
import "github.com/fastabc/fastconf/observe"

slack := observe.Async(observe.Func(func(ctx context.Context, e fastconf.Event) {
    c, ok := e.(fastconf.Committed)
    if !ok || c.Prev == 0 { // skip the initial load
        return
    }
    lines := make([]string, 0, len(c.Diff()))
    for _, d := range c.Diff() {
        lines = append(lines, fmt.Sprintf("%s %s: %v -> %v", d.Change, d.Path, d.Before, d.After))
    }
    body, _ := json.Marshal(map[string]any{
        "text": fmt.Sprintf("config reloaded: gen %d → %d (%s)\n```\n%s\n```",
            c.Prev, c.Next, c.Cause.Reason, strings.Join(lines, "\n")),
    })
    req, _ := http.NewRequestWithContext(ctx, "POST", os.Getenv("SLACK_WEBHOOK"), bytes.NewReader(body))
    if resp, err := http.DefaultClient.Do(req); err == nil {
        resp.Body.Close()
    }
}), 64)
defer slack.Close() // after mgr.Close, so queued events drain

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithObserver(observe.Multi(observe.JSONLines(os.Stderr), slack)),
)
defer mgr.Close()
```

`Committed.Diff()` computes the redacted per-path diff on first call and
caches it; observers that never call it pay nothing. A full `Async` queue
drops the event and increments `Dropped()`.

## Building blocks

| Helper | Purpose |
|---|---|
| `observe.Func(fn)` | Adapt a function |
| `observe.Multi(obs...)` | Fan out in order |
| `observe.Async(obs, queueCap)` | Bounded queue on its own goroutine |
| `observe.JSONLines(w)` | One JSON audit line per commit: reason, time, generations, revisions, tenant |
| `observe.Metrics(sink)` | Adapt the Prometheus sink (`observability/metrics/prometheus`) |

## When Committed does not fire

- Hash-dedupe skipped the swap (no semantic change).
- The reload failed — observers see `ReloadFinished{Err}`; `Manager.Errors()`
  carries the same failure.

## fastconfctl diff

`fastconfctl diff -from=dev -to=prod` is the offline counterpart — it loads
two configurations and prints the same dotted-path diff format used by
`State.Diff`.
