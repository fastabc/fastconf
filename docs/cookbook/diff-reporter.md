# Diff reporter

Every successful reload produces a `State.Diff(other)` slice of dotted-path lines. `WithDiffReporter` wires that slice into an external sink (Slack bot, PagerDuty, GitHub PR comment, …) so operators see *exactly* what changed without staring at log lines.

## Async by design

Reporters run on a fresh goroutine after the atomic state swap and after `AuditSink` fan-out. A slow Slack call cannot inflate reload commit time, and a reporter failure is logged but never aborts the reload.

## Wire it

```go
import "github.com/fastabc/fastconf"

slack := fastconf.DiffReporterFunc(func(ctx context.Context, ev fastconf.DiffEvent) error {
    body := map[string]any{
        "text": fmt.Sprintf("config reloaded: gen %d → %d (%s)\n```\n%s\n```",
            ev.PrevGeneration, ev.NewGeneration, ev.Reason,
            strings.Join(ev.Diff, "\n")),
    }
    b, _ := json.Marshal(body)
    req, _ := http.NewRequestWithContext(ctx, "POST", os.Getenv("SLACK_WEBHOOK"), bytes.NewReader(b))
    _, err := http.DefaultClient.Do(req)
    return err
})

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithDiffReporter(slack),
)
```

## Event shape

```go
type DiffEvent struct {
    Reason         string         // "watcher", "manual", "provider:vault://..."
    PrevGeneration uint64
    NewGeneration  uint64
    At             time.Time
    Diff           []string       // human-readable lines: "+ a.b = 1", "~ c.d : x -> y", "- e.f"
    Cause          ReloadCause    // full audit trail (revisions, tenant, …)
}
```

## When reporters DON'T fire

- Hash-dedupe skipped the swap (no semantic change).
- First reload (no previous state to diff against).
- Reload failed — `m.Errors() consumer` is the hook for that path.

## fastconfctl diff

`fastconfctl diff -from=dev -to=prod` is the offline counterpart — it loads two configurations and prints the same dotted-path diff format used by `State.Diff`. No reporter wiring needed.
