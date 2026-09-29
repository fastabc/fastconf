# Cross-process push (NATS / Redis Streams)

For cross-process configuration updates, the repository includes two transport
provider reference implementations. Delivery latency depends on the transport
and reload pipeline:

| Provider | Transport | Use when |
|------------|-----------|----------|
| `examples/nats`         | NATS subject subscribe + JetStream resume | you already run NATS/NATS JetStream |
| `examples/redisstream`  | Redis `XREAD BLOCK` + stream id resume | you already run Redis 5+ |

These examples take a transport client from the caller and add no client dependency.
Copy and adapt them for production; they are example packages, outside the stable provider API.
Their README files describe the shared helper to copy when moving outside this repository.

## Dependency-free contracts

Neither package imports `github.com/nats-io/nats.go` or `github.com/redis/go-redis/v9` directly. They define a small `Conn` / `Client` interface and you wire in your real client through an application adapter. This keeps the providers testable with mocks and lets you choose the exact client version.

## NATS adapter

```go
import (
    "errors"

    "github.com/fastabc/fastconf/codec"
    natsgo "github.com/nats-io/nats.go"
    natsprov "github.com/fastabc/fastconf/examples/nats"
)

type natsAdapter struct{ nc *natsgo.Conn }

func (a natsAdapter) Subscribe(subject string, h func(natsprov.Msg)) (natsprov.Subscription, error) {
    sub, err := a.nc.Subscribe(subject, func(m *natsgo.Msg) {
        h(natsprov.Msg{Subject: m.Subject, Data: m.Data})
    })
    return sub, err
}
func (a natsAdapter) SubscribeFrom(subject, _ string, h func(natsprov.Msg)) (natsprov.Subscription, error) {
    // Plain NATS cannot replay: the provider falls back to Subscribe and
    // marks the first event with Gap.
    return nil, errors.New("resume not supported")
}

decoder, err := codec.For("yaml")
if err != nil {
    return err
}
nc, err := natsgo.Connect("nats://localhost")
if err != nil {
    return err
}
defer nc.Close()
p, err := natsprov.New("nats", "fastconf.app", decoder, natsAdapter{nc})
if err != nil {
    return err
}
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(p),
    fastconf.WithWatch(fastconf.Watch{}),
)
```

## Redis Streams adapter

```go
import (
    "context"
    "errors"
    "time"

    "github.com/redis/go-redis/v9"
    rsprov "github.com/fastabc/fastconf/examples/redisstream"
)

type rdbAdapter struct{ c *redis.Client }

func (a rdbAdapter) XRead(ctx context.Context, stream, lastID string, block time.Duration) ([]rsprov.Entry, error) {
    res, err := a.c.XRead(ctx, &redis.XReadArgs{
        Streams: []string{stream, lastID}, Block: block, Count: 64,
    }).Result()
    if errors.Is(err, redis.Nil) { return nil, nil } // idle blocking-read timeout
    if err != nil { return nil, err }
    var out []rsprov.Entry
    for _, s := range res {
        for _, m := range s.Messages {
            fields := map[string]string{}
            for k, v := range m.Values { fields[k], _ = v.(string) }
            out = append(out, rsprov.Entry{ID: m.ID, Fields: fields})
        }
    }
    return out, nil
}
```

## Resume

The framework remembers the last `Event.Revision` per provider and passes it
to `Watch(ctx, from)` on reconnect. JetStream / Redis Streams resume natively.
When `SubscribeFrom` fails, the NATS provider subscribes cold and sets
`Event.Gap` on the first event; the manager counts it as a provider error and
reloads the latest snapshot. Intermediate changes during the gap may be lost.

## Drop-on-full

Subscriptions push events into a buffered channel; if a downstream reload is slow, additional events are *dropped* rather than blocking the transport. The writer serializes accepted reload requests; dropped notifications are not
replayed. These providers retain the latest payload, so a later reload can
converge to it without delivering every intermediate configuration.

## Runnable example

[`ExampleWithProvider`](../../example_api_test.go) combines a structured
provider with an inline `source.NewBytes` document through `WithProvider`,
the same entry point used by the NATS / Redis adapters above.
Run it with `go test . -run '^ExampleWithProvider$' -v`.
