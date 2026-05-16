# OpenTelemetry tracing

Wire the OTel SDK once, then every reload emits spans:

```
fastconf.reload
  └─ fastconf.assemble
  └─ fastconf.commit
       ├─ fastconf.merge
       ├─ fastconf.migration
       ├─ fastconf.transform
       ├─ fastconf.decode
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
```

Each span carries the same `reason` attribute that audit logs use,
so trace ↔ log correlation is automatic.
