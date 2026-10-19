# Configuration policies

Policies run after decode, field constraints and validators, before publication.
Warnings allow a reload; `SeverityError` or evaluation errors reject it and
preserve the current snapshot. Plan instead collects findings, including policy
evaluation failures: a nil Plan error is not an acceptance decision.

## Inline Go policy

This fragment imports `context`, `github.com/fastabc/fastconf` and
`github.com/fastabc/fastconf/policy`. `MyApp` has `Profile` and `Debug` fields:

```go
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithPolicy(policy.Func[MyApp]{
        N: "no-debug-in-prod",
        Fn: func(_ context.Context, in policy.Input[MyApp]) ([]policy.Violation, error) {
            if in.Config.Profile == "prod" && in.Config.Debug {
                return []policy.Violation{{
                    Path: "debug", Message: "debug is disabled in prod",
                    Severity: policy.SeverityError,
                }}, nil
            }
            return nil, nil
        },
    }),
)
if err != nil {
    return err
}
defer mgr.Close()
```

The profile field here belongs to the application; selecting a FastConf overlay
does not populate `MyApp.Profile` automatically. Policy input also includes the
reload reason and tenant label.

## Optional OPA and CUE adapters

Install only the chosen [optional module](../../getting-started/extensions.md).
The following fragments compile a policy before registering it; handle compilation
errors before calling `New`.

OPA import: `github.com/fastabc/fastconf/policy/opa`. `MyApp` must expose `tls`
in its JSON representation:

```go
p, err := opa.New[MyApp]("compliance", `package fastconf
deny[msg] { input.config.tls == false; msg := "TLS must be on" }`)
if err != nil {
    return err
}
```

The default query is `data.fastconf.deny`. Each returned denial string becomes an
error-severity violation. `NewWithQuery` selects an application-specific query.
See its [contract tests](../../../policy/opa/opa_test.go).

CUE import: `cuepol "github.com/fastabc/fastconf/cue/policy"`:

```go
p, err := cuepol.New[MyApp]("ports", `port: >0 & <65536`)
if err != nil {
    return err
}
```

CUE validates the typed config's JSON representation against the schema;
unification failures become error-severity violations. It does not decrypt
secrets or select overlays.

For either compiled adapter:

```go
mgr, err := fastconf.New[MyApp](ctx, fastconf.WithPolicy(p))
if err != nil {
    return err
}
defer mgr.Close()
```

A complete Go policy, rejection, warning and recovery scenario is in
[`examples/reload`](../../../examples/reload/README.md). See [Plan](../runtime/plan.md) for
checking reports before attempting publication.
