# Periodic reconciliation and supervised notifications

Run from the repository root (Go 1.25+):

```sh
GOPROXY=direct go run ./examples/cloud-reload
GOPROXY=direct go test -race ./examples/cloud-reload
```

[main.go](main.go) exercises a real Manager with an in-memory provider and a
`contracts.Notifier` implementation. Expected output:

```text
recovered without notification: 2
paused: acknowledged=false
redelivered: value=3 acknowledged=true restarted=true
```

`WithNotifier` makes Manager supervise `consumer.Run`, restarting an early return
or panic with cancelable 250ms–30s backoff and jitter. The fixture disconnects once,
then accepts deliveries. Call `refresh(ctx)` before acknowledging a message;
**only nil permits ACK**. `ErrPaused` means the update was skipped and must remain
available for redelivery. Resume and redeliver it to load the latest configuration.

`WithResync` repairs changes even when no notification arrives. Replace the demo's
10ms interval with one appropriate to cloud quotas, and alert on persistent
errors. `Pause` skips new background refreshes but does not cancel work already
queued or running; it is not a publication barrier. Explicit Reload/Plan still run.

Closing Manager cancels and joins both workers before the application closes
borrowed SDK clients. The constructor context bounds initialization; application
shutdown must call Close/Shutdown explicitly.

This proves local retry, missed-event recovery and refresh-before-ACK behavior,
not cloud routing. See the [notification cookbook](../../docs/cookbook/sources/cloud-notifications.md)
for dedicated subscriptions, credentials, delivery deadlines and dead-letter setup.
