# Observability runbook

FastConf emits reload, provider-watch, dropped-event, generation, layer-count,
stage-duration, audit, and trace signals. Treat these as a single reload health
surface rather than independent dashboards.

## Prometheus

Import the first-party Prometheus module and register the sink with the same
registry your service already exposes:

```go
import (
    "github.com/prometheus/client_golang/prometheus"

    fastconf "github.com/fastabc/fastconf"
    fcprom "github.com/fastabc/fastconf/observability/metrics/prometheus"
)

mgr, err := fastconf.New[Config](ctx,
    fastconf.WithMetrics(fcprom.New(prometheus.DefaultRegisterer)),
)
```

Ship both rule files from the module:

| File | Purpose |
|---|---|
| `observability/metrics/prometheus/recording_rules.yaml` | SLI views: reload success ratio, reload p99, stage p99, provider error rate, dropped event rate |
| `observability/metrics/prometheus/alert_rules.yaml` | Alerts for reload failures, slow reloads, provider watch errors, and dropped provider events |

## Triage

| Symptom | First checks |
|---|---|
| `FastConfReloadFailures` | Inspect `Manager.Errors()`, audit sink output, validation/policy failures, and provider decode errors |
| `FastConfReloadLatencyHigh` | Break down `fastconf_stage_duration_seconds` by `stage`; slow `validate` or `policy` usually means user code is blocking reload |
| `FastConfProviderWatchErrors` | Check provider credentials, network reachability, revision retention, and whether `WatchFrom` can resume from the stored revision |
| `FastConfProviderEventsDropped` | The reload queue is saturated; reduce upstream event volume, increase coalescing before FastConf, or fix slow reload stages |

## OpenTelemetry

Use [otel.md](otel.md) for SDK wiring. Trace spans carry the reload `reason`,
which is the join key to audit logs and `fastconf_reload_total{result=...}`.
