# Redis Streams provider reference

An interface-based example of a cached configuration provider using blocking
stream reads. It is not a standalone command or a supported Redis client adapter.

- [Implementation](redisstream.go): provide `Client.XRead`, honoring context
  cancellation. The default payload field is `payload` and block interval is 5s.
- [Tests](redisstream_test.go): use transport doubles; no Redis service is needed.
- [Shared helper](../../internal/providerutil/providerutil.go): copy `Latest` and
  `Offer` into an application-owned helper when copying the provider. External
  applications cannot import this repository's `internal` package.

An empty cursor starts at `$`, so existing entries do not supply initial state.
Before the first valid entry, `Load` returns an empty stale snapshot. Supply
valid base configuration or implement an initial fetch. Reconnects can resume
from a stream ID, but trimmed-history gaps are not detected automatically.
Malformed/missing payloads are skipped; full notification channels increment
`Dropped()`. Delivery does not guarantee every intermediate configuration.

Run from the repository root (Go 1.24+):

```sh
go test -race ./examples/redisstream
```

See [cross-process updates](../../docs/cookbook/sources/cross-process.md) for integration
and delivery limits.
