# Feature flags & rollouts

FastConf carries a tiny rule engine (`feature`). Feature rules are an ordinary field of your config struct, so they ride the lock-free `*T` snapshot: `feature.Eval(mgr.Get().Features, key, ctx, def)` is the request-path entry point. `Get()` is one atomic load; evaluation walks targets and rollouts in order and hashes the rollout anchor when needed.

## Rule shape

```yaml
features:
  darkMode:
    default: false
    targets:                       # exact-match overrides, first wins
      - when: {region: "eu-west"}
        value: true
    rollouts:                      # percentage bucketing, evaluated after targets
      - percent: 30
        hashKey: "user.id"
        value: true
```

`Rule.Evaluate` is deterministic for the same rule and evaluation context.

Every attribute named in a target's `when` must be present and equal. For example,
`when: {tier: ""}` matches an explicitly empty `tier`, but does not match a context
that omits `tier`. An empty `when` never matches.

## Wire it

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/feature"
)

type AppConfig struct {
    Features map[string]feature.Rule `json:"features" yaml:"features"`
}

mgr, _ := fastconf.New[AppConfig](ctx, fastconf.WithDir("conf.d"))

// In a request handler:
on, _ := feature.Eval(mgr.Get().Features, "darkMode", feature.EvalContext{
    "region":  "eu-west",
    "user.id": "u_42",
}, false).(bool)
```

## Runnable example

[`examples/features`](../../examples/features/README.md) includes complete YAML,
targeting, a 30% rollout, missing-key fallbacks and deterministic output:

```sh
go run ./examples/features
```

The `feature` package is a local rule evaluator. It does not implement an
OpenFeature SDK provider. Hashing uses the anchor alone; separate flag cohorts
need a flag namespace in the supplied anchor.
