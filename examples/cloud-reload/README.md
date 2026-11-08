# Periodic reconciliation and supervision

Run from the repository root (Go 1.25+):

```sh
go run ./examples/cloud-reload
go test -race ./examples/cloud-reload
```

[main.go](main.go) loads an in-memory source through a real Manager, changes it
once and deliberately omits its notification. A bounded periodic Reload repairs
the stale value. Expected output: `recovered without notification: 2`.

The application-owned `supervise` loop restarts a notification consumer after
receive errors, with a cancelable 1–2 second delay. Replace its local receiver
with `consumer.Run(ctx, func(work context.Context) error { return mgr.Reload(work) })`. The separate `reconcile` loop uses jitter
and a 30 second reload deadline; replace the demo's 10 ms period with a value
appropriate to cloud quotas (for example, one minute). Alert on persistent
failures. Cancel both loops and join them before closing Manager and SDK clients.

This proves local recovery from a missing event, not cloud routing. See the
[notification cookbook](../../docs/cookbook/sources/cloud-notifications.md) for
dedicated subscriptions, credentials, delivery deadlines and dead-letter setup.

Manager 使用 `WithNotifier` 托管消费生命周期，`WithResync` 托管周期对账；
示例故意漏掉更新通知，依靠 10ms 对账恢复。关闭 Manager 会等待这些 worker。
