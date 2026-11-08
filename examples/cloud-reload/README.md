# Periodic reconciliation and supervision

Run from the repository root (Go 1.25+):

```sh
go run ./examples/cloud-reload
go test -race ./examples/cloud-reload
```

[main.go](main.go) loads an in-memory source through a real Manager, changes it
once and deliberately omits its notification. A bounded periodic Reload repairs
the stale value. Expected output: `recovered without notification: 2`.

`WithNotifier` makes Manager supervise `consumer.Run`, restarting an early return
or panic with cancelable 250ms–30s backoff and jitter. `WithResync` supplies the
independent periodic recovery path. Replace the demo's 10ms period with one
appropriate to cloud quotas, and alert on persistent errors. Closing Manager
cancels and joins both workers before the application closes borrowed SDK clients.

This proves local recovery from a missing event, not cloud routing. See the
[notification cookbook](../../docs/cookbook/sources/cloud-notifications.md) for
dedicated subscriptions, credentials, delivery deadlines and dead-letter setup.
