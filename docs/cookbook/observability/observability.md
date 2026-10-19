# Observability runbook

FastConf exposes reload results, stage timings, provider notifications and
commit events. Optional adapters translate them into metrics and traces.
Commit audit lines do not contain reload failures; use `Errors()` or
`ReloadFinished` for those.

## Prometheus

Import the first-party Prometheus module and register the sink with the same
registry your service already exposes:

```go
import (
    "github.com/prometheus/client_golang/prometheus"

    fastconf "github.com/fastabc/fastconf"
    fcprom "github.com/fastabc/fastconf/observability/metrics/prometheus"
    "github.com/fastabc/fastconf/observe"
)

mgr, err := fastconf.New[Config](ctx,
    fastconf.WithObserver(observe.Metrics(fcprom.New(prometheus.DefaultRegisterer))),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Ship both rule files from the module:

| File | Purpose |
|---|---|
| `observability/metrics/prometheus/recording_rules.yaml` | SLI views: reload success ratio, reload p99, stage p99, provider error rate, dropped event rate |
| `observability/metrics/prometheus/alert_rules.yaml` | Alerts for reload failures, slow reloads, provider watch errors, and dropped provider events |

## Triage

| Symptom | First checks |
|---|---|
| `FastConfReloadFailures` | Inspect `Manager.Errors()` or `ReloadFinished.Err`; correlate with commit audit lines and validation/provider errors |
| `FastConfReloadLatencyHigh` | Break down `fastconf_stage_duration_seconds` by `stage`; slow `validate` or `policy` usually means user code is blocking reload |
| `FastConfProviderWatchErrors` | Check provider credentials, network reachability, revision retention, and whether `Watch(ctx, from)` can resume from the stored revision |
| `FastConfProviderEventsDropped` | The reload queue is saturated; reduce upstream event volume, increase coalescing before FastConf, or fix slow reload stages |

## OpenTelemetry

Configure the OTel SDK, then register the tracer. A normal reload emits this
span tree; stages after a failure are skipped, and unchanged-input shortcuts
may omit pipeline stages:

```
fastconf.reload
  ├─ fastconf.assemble
  └─ fastconf.commit
       ├─ fastconf.merge
       ├─ fastconf.transform
       ├─ fastconf.secret
       ├─ fastconf.typed-hooks
       ├─ fastconf.decode
       ├─ fastconf.field-meta
       ├─ fastconf.validate
       └─ fastconf.policy
```

```go
import (
    fcotel "github.com/fastabc/fastconf/observability/otel"
    "go.opentelemetry.io/otel"
)

tp := newTracerProvider()    // your existing OTel setup
otel.SetTracerProvider(tp)

mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithTracer(fcotel.New(otel.Tracer("fastconf"))),
)
if err != nil {
    return err
}
defer mgr.Close()
```

The root reload span has a `reason` attribute; pipeline-stage spans use
`fastconf.reload.reason`. Audit lines carry the same reason. Prometheus reload
counters have only a `result` label; they do not expose reason, tenant or trace
IDs. Correlate an incident by its time window and audit/trace context.

Register the Prometheus sink once per registry and share it through an observer
when instrumenting multiple managers. Calling `fcprom.New` twice on the same
registry attempts duplicate metric registration and panics. With a shared
sink, generation and layer-count gauges reflect the most recently emitting
manager; they are not per-tenant values or sums across managers.
