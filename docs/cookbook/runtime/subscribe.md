# Subscribe to selected configuration changes

`Subscribe` compares a selected value after publication, including rollback.
Use it for small synchronous updates or to signal a bounded resource worker.
Use [observers](../observability/observer.md) for every reload attempt, failures or commit audit.

Given an initialized `mgr` whose config has `Server.Port`:

```go
cancel := fastconf.Subscribe(mgr,
    func(c *Config) *int { return &c.Server.Port },
    func(old, next *int) {
        log.Printf("port changed: %d -> %d", *old, *next)
    },
)
defer cancel()
```

This fragment imports `log` and `github.com/fastabc/fastconf`. It does not call
the callback at registration: read `mgr.Get()` to initialize application state.
Default equality is `reflect.DeepEqual` on the extracted values. `WithEqual`
can replace it; nil/non-nil transitions always trigger and two nil values do not.
If the extractor can return nil, handle nil in the callback too.

## Callback boundaries

Callbacks run synchronously in registration order on the writer. Return
quickly. Do not wait for the same manager's `Reload`, `Plan`, rollback, `Close`
or `Shutdown` from a callback. The extracted values belong to read-only snapshots.
A panic in the extractor, comparator or callback is recovered and reported on
`Errors()` with reason `subscriber-panic`; the snapshot has already published
and is not rolled back.

Equal typed hashes skip subscribers on reload. A change to resolver-sensitive
paths still publishes a metadata commit for diagnostics/history/audit, but does
not invoke value subscribers. Even a comparator that always returns false cannot
observe equal-value reloads; use an observer for their lifecycle events.

## Updating connections or other resources

Signal a bounded worker, then let it read the latest snapshot, build with a
timeout, retain the old resource on failure and drain old users before closing
it. Configuration publication and resource replacement are separate operations.
See the complete [resource-update example](../../../examples/resource-update/README.md)
for ownership, failed builds, coalescing and shutdown:

```sh
go run ./examples/resource-update
go test -race ./examples/resource-update
```

The [dynamic example](../../../examples/dynamic/README.md) demonstrates file
watching, lightweight callbacks, rejected input and manager shutdown.

## Subtree subscriptions: separate database and logging updates

```go
cancelDB := fastconf.Subscribe(mgr,
    func(c *Config) *DBConfig { return &c.Database },
    func(old, next *DBConfig) { reconnectQueue.Signal() },
    fastconf.WithEqual(func(old, next *DBConfig) bool { return old.DSN == next.DSN }),
)
defer cancelDB()
cancelLog := fastconf.Subscribe(mgr,
    func(c *Config) *LogConfig { return &c.Log },
    func(old, next *LogConfig) { logger.SetLevel(next.Level) },
)
defer cancelLog()
```

Changing only the logging subtree does not invoke the database callback.
Here `WithEqual` also excludes pool-size changes from reconnection; without it,
the entire Database subtree is compared. Delegate slow reconnection to a bounded
worker and only signal from callbacks. `Subscribe` is a package-level generic
function because Go methods cannot declare type parameters, keeping both the
selector and callback statically typed.

For path-prefix auditing, use an observer with the masked commit diff:

```go
type audit struct{}
func (audit) Observe(ctx context.Context, ev fastconf.Event) {
    if committed, ok := ev.(fastconf.Committed); ok {
        for _, d := range committed.Diff() {
            if d.Path == "database" || strings.HasPrefix(d.Path, "database.") {
                log.Printf("database config changed: %s", d.Path)
            }
        }
    }
}
// fastconf.WithObserver(audit{})
```

See `ExampleSubscribe_subtree` in the root package for an executable API example.
