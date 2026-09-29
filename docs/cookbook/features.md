# Feature flags & rollouts

FastConf carries a tiny rule engine (`feature`). Feature rules are an ordinary field of your config struct, so they ride the lock-free `*T` snapshot: `feature.Eval(mgr.Get().Features, key, ctx, def)` is the request-path entry point — one `atomic.Pointer.Load`, one map lookup, one deterministic compare/hash, no allocation. Safe for the hottest handler.

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

`Rule.Evaluate` is pure: same `(Rule, ctx)` always returns the same value, so callers can cache the bucket if they need to.

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

## Things FastConf *won't* do

- Run a separate flag service (the table lives in your YAML / overlays).
- Sticky user → bucket assignments (`Rollout.Evaluate` is deterministic per anchor; persist server-side if you need history).
- Schema migration of rule bodies (use a `WithTransform` migration).

## OpenFeature compatibility
