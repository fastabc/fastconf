# Bounded resource updates

Run `go run ./examples/resource-update` from the repository root (Go 1.24+).
This executable models connection ownership without needing a database:

```text
desired=1 applied=1 active=A failed=false
desired=2 applied=1 active=A failed=true
desired=3 applied=3 active=C failed=false
```

The Config is already published when Subscribe runs. One capacity-1 notification
channel wakes one worker, which reads the latest snapshot and builds with a
timeout. A failed B connection keeps A serving; C then succeeds. Replace the
model builder with a real connector that honors context, and deep-copy config
collections before handing them to a library that may mutate them.

Intermediate configurations may be coalesced. A build for an outdated generation
is closed and retried against the latest snapshot. The final generation check
reduces stale application; it cannot atomically commit both config and resource.
Failures are reported with desired/applied generations, and recovery requires a
new notification or application-triggered retry. There is no unbounded retry loop.
Reporting is also bounded and may drop samples; it does not drive convergence.

The slot holds a read lock for the duration of a business request. Replacement
waits for existing readers before closing the old resource. Real applications
may use their pool's own draining or reference mechanism instead. Requests must
finish for replacement/shutdown to finish.

Shutdown: close Manager, unsubscribe, cancel and join the worker, then drain
requests and close resources. Do not close the notification channel while a
callback may send to it. For telemetry alone, use `observe.Async` and inspect
`Dropped()`; close that observer after the Manager. Its drop-on-full FIFO alone
does not guarantee a final resource update.

Source: [main.go](main.go).

Verify from the repository root: `go test -race ./examples/resource-update`.
