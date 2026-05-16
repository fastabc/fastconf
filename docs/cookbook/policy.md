# Policy engine

Policies run AFTER decode + validate but BEFORE atomic state swap.
A `SeverityError` aborts the reload; the previous `*State[T]` stays
in place — readers see no glitch.

## Inline Go policy

```go
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithPolicy(policy.Func[MyApp]{
        N: "no-debug-in-prod",
        Fn: func(_ context.Context, in policy.Input[MyApp]) ([]policy.Violation, error) {
            if in.Config.Profile == "prod" && in.Config.Debug {
                return []policy.Violation{{Path: "debug", Severity: policy.SeverityError}}, nil
            }
            return nil, nil
        },
    }),
)
```

## OPA / Rego (opt-in submodule)

```go
import "github.com/fastabc/fastconf/policy/opa"

p, _ := opa.New[MyApp]("compliance", `package fastconf
deny[msg] { input.config.tls == false; msg := "TLS must be on" }`)
mgr, _ := fastconf.New[MyApp](ctx, fastconf.WithPolicy(p))
```

## CUE schema (opt-in submodule)

```go
import cuepol "github.com/fastabc/fastconf/policy/cue"

p, _ := cuepol.New[MyApp]("ports", `port: >0 & <65536`)
mgr, _ := fastconf.New[MyApp](ctx, fastconf.WithPolicy(p))
```
