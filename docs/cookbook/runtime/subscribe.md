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

Equal typed hashes skip publication before subscribers run. Even a comparator
that always returns false cannot observe those no-op reloads.

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
