# Feature flags & rollouts

FastConf carries a tiny rule engine (`pkg/feature`) that piggy-backs on the lock-free `*State[T]` snapshot. `fastconf.Eval[T,V](mgr, key, ctx, def)` is the request-path entry point — one `atomic.Pointer.Load`, one map lookup, one deterministic compare/hash. The result is typed (no `any` cast). Safe for the hottest handler.

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

## Wire it

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/feature"
)

type AppConfig struct {
    Features map[string]feature.Rule `json:"features" yaml:"features"`
}

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithFeatureRules[AppConfig](func(c *AppConfig) map[string]feature.Rule {
        return c.Features
    }),
)

// In a request handler:
on := fastconf.Eval[AppConfig, bool](mgr, "darkMode", feature.EvalContext{
    "region":  "eu-west",
    "user.id": "u_42",
}, false)
```

## Things FastConf *won't* do

- Run a separate flag service (the table lives in your YAML / overlays).
- Sticky user → bucket assignments (`Rollout.Evaluate` is deterministic per anchor; persist server-side if you need history).
- Schema migration of rule bodies (use `WithMigrations`).

## OpenFeature compatibility

`integrations/openfeature` adapts the same rule table into an OpenFeature-shaped provider — see [openfeature.md](openfeature.md). It uses `state.FeatureRules()` + `feature.Eval` directly, since the OF spec requires the untyped any-returning shape per evaluation kind.
