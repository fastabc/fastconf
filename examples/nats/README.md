# NATS provider reference

An interface-based example of a cached configuration provider with transport
notifications. It is not a standalone command or a supported NATS client adapter.

- [Implementation](nats.go): provide `Conn.Subscribe` and `SubscribeFrom`; replay
  and revisions depend on the adapter. Failed resume falls back cold with `Gap`.
- [Tests](nats_test.go): use transport doubles; no NATS service is needed.
- [Shared helper](../../internal/providerutil/providerutil.go): copy `Latest` and
  `Offer` into an application-owned helper when copying the provider. External
  applications cannot import this repository's `internal` package.

Before the first valid message, `Load` returns an empty stale snapshot. Supply
valid base configuration or implement an initial fetch. Malformed documents are
skipped; full notification channels increment `Dropped()` without blocking the
transport. Delivery does not guarantee every intermediate configuration.

Run from the repository root (Go 1.24+):

```sh
go test -race ./examples/nats
```

See [cross-process updates](../../docs/cookbook/sources/cross-process.md) for integration
and delivery limits.
