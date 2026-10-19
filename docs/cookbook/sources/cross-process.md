# Cross-process updates: NATS and Redis Streams

The repository includes interface-based transport reference implementations,
not stable provider packages or ready-to-run client integrations:

| Reference | Application adapter | Resume behavior |
|---|---|---|
| [NATS](../../../examples/nats/README.md) | Implement `Conn.Subscribe` and `SubscribeFrom` | Adapter supplies revisions/replay; a failed resume falls back to a cold subscription and marks the first event `Gap` |
| [Redis Streams](../../../examples/redisstream/README.md) | Implement `Client.XRead` | Empty cursor starts at `$`; reconnects can pass a stream ID |

Neither package imports a real NATS or Redis client. Copy the implementation
and its shared helper into the application, then adapt the chosen client.
The per-example README links to the exact source and helper to copy.

## Initial state

`Load` returns an empty, stale snapshot until the transport receives its first
valid document. The manager loads before starting provider watchers, so these
references cannot supply a required initial configuration by themselves. Provide
a valid base layer or implement an application-specific initial fetch. The
Redis reference starts at the tail; it does not load an existing latest entry.

## Register an adapted provider

For the NATS reference, this fragment assumes `conn` implements the example's
`Conn` interface and `Config` has valid defaults in `conf.d`:

```go
decoder, err := codec.For("yaml")
if err != nil {
    return err
}
p, err := natsprov.New("nats", "fastconf.app", decoder, conn)
if err != nil {
    return err
}
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(p),
)
if err != nil {
    return err
}
defer mgr.Close()
// Keep the manager alive while transport events are consumed.
```

Imports: `github.com/fastabc/fastconf`, `github.com/fastabc/fastconf/codec`
and `natsprov "github.com/fastabc/fastconf/examples/nats"` when running inside
this repository. Application-owned copies use their own import path.
`WithWatch` is needed only for local file watching, not transport events.

## Delivery limits

The manager passes the last non-empty `Event.Revision` to `Watch(ctx, from)`
on reconnect. Replay depends on the adapter and upstream retention; a cold
subscription may lose intermediate changes. The Redis reference does not detect
trimmed history or report such gaps automatically.

Both references cache the latest successfully decoded document before offering
an event on a bounded channel. Full channels drop new notifications and count
`Dropped()`; malformed payloads are skipped. These losses are distinct from
the manager's reload-queue `EventDropped` events and compensating refresh.
A later reload reads the cached latest document, but the references do not
guarantee every intermediate configuration or automatic final delivery.

## Verify locally

No external service is needed for the transport contract tests:

```sh
go test -race ./examples/nats ./examples/redisstream
```

See the [provider contract](../../design/spec.md#layer-order) for snapshot ownership,
watcher revisions and source precedence.
