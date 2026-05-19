# Cross-process push (NATS / Redis Streams)

`integrations/bus` ships an in-process Broker. For **cross-process** push (gateway updates config → 50 workers learn within a second) FastConf provides two sub-modules:

| Sub-module | Transport | Use when |
|------------|-----------|----------|
| `providers/nats`         | NATS subject subscribe + JetStream resume | you already run NATS/NATS JetStream |
| `providers/redisstream`  | Redis `XREAD BLOCK` + stream id resume | you already run Redis 5+ |

Both are sub-modules: their import only enters your closure if you opt in.

## Dependency-free contracts

Neither package imports `github.com/nats-io/nats.go` or `github.com/redis/go-redis/v9` directly. They define a small `Conn` / `Client` interface and you wire in your real client through a 5-line adapter. This keeps the providers testable with mocks and lets you choose the exact client version.

## NATS adapter

```go
import (
    natsgo "github.com/nats-io/nats.go"
    natsprov "github.com/fastabc/fastconf/providers/nats"
)

type natsAdapter struct{ nc *natsgo.Conn }

func (a natsAdapter) Subscribe(subject string, h func(natsprov.Msg)) (natsprov.Subscription, error) {
    sub, err := a.nc.Subscribe(subject, func(m *natsgo.Msg) {
        h(natsprov.Msg{Subject: m.Subject, Data: m.Data})
    })
    return sub, err
}
func (a natsAdapter) SubscribeFrom(subject, _ string, h func(natsprov.Msg)) (natsprov.Subscription, error) {
    // For non-JetStream NATS, return contracts.ErrResumeUnsupported.
    return nil, contracts.ErrResumeUnsupported
}

nc, _ := natsgo.Connect("nats://localhost")
p, _ := natsprov.New("nats", "fastconf.app", yamlCodec{}, natsAdapter{nc})
mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProvider(p),
    fastconf.WithWatch(true),
)
```

## Redis Streams adapter

```go
import (
    "context"
    "time"

    "github.com/redis/go-redis/v9"
    rsprov "github.com/fastabc/fastconf/providers/redisstream"
)

type rdbAdapter struct{ c *redis.Client }

func (a rdbAdapter) XRead(ctx context.Context, stream, lastID string, block time.Duration) ([]rsprov.Entry, error) {
    res, err := a.c.XRead(ctx, &redis.XReadArgs{
        Streams: []string{stream, lastID}, Block: block, Count: 64,
    }).Result()
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

## Resumable

Both providers implement `contracts.Resumable`. The framework remembers the last `Event.Revision` per provider; on reconnect it calls `WatchFrom(ctx, lastRev)`. JetStream / Redis Streams natively support this — return `contracts.ErrResumeUnsupported` for transports that can't.

## Drop-on-full

Subscriptions push events into a buffered channel; if a downstream reload is slow, additional events are *dropped* rather than blocking the transport. FastConf's single-writer reload loop preserves order across drops.

## Runnable example

[`examples/external_source/example_test.go`](../../examples/external_source/example_test.go) —
a stand-alone Provider plus an inline `WithSource(seed, parser.YAML())`
byte-blob layer, the same two extension points used by the NATS / Redis
adapters above. Run it with `go test ./examples/external_source/...`.
