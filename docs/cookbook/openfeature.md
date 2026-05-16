# FastConf as an OpenFeature provider

`integrations/openfeature` is a tiny adapter that exposes a FastConf `Manager` through an OpenFeature-shaped `Provider` interface. Projects already wired to the OpenFeature SDK can swap ConfigCat / LaunchDarkly / Unleash for FastConf without changing call sites.

The package **does not import** `github.com/open-feature/go-sdk`, so installing it does not force the SDK closure on every caller. A 20-line shim in your own code adapts the local resolution structs to the SDK's `ResolutionDetail` types.

## Wire it

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/integrations/openfeature"
    "github.com/fastabc/fastconf/pkg/feature"
)

type AppConfig struct {
    Features map[string]feature.Rule `json:"features"`
}

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithFeatureRules[AppConfig](func(c *AppConfig) map[string]feature.Rule {
        return c.Features
    }),
)

p := openfeature.New(mgr, "" /* KeyRoot */)
got := p.BooleanEvaluation("darkMode", false, openfeature.EvaluationContext{
    "region":  "eu-west",
    "user.id": "u_42",
})
fmt.Println(got.Value, got.Reason) // true TARGETING_MATCH
```

## KeyRoot

`KeyRoot` is an optional prefix prepended to every flag lookup. Use it when your rule table is flat-keyed with a namespace (`features.darkMode`); leave it empty when the extractor returns a map keyed by short names (the common shape).

## Adapting to the upstream SDK

```go
import (
    "context"
    of "github.com/open-feature/go-sdk/pkg/openfeature"
    fcopen "github.com/fastabc/fastconf/integrations/openfeature"
)

type bridge struct{ inner *fcopen.Provider }

func (b *bridge) BooleanEvaluation(ctx context.Context, flag string, def bool, evalCtx of.EvaluationContext) of.BoolResolutionDetail {
    inner := b.inner.BooleanEvaluation(flag, def, toLocalCtx(evalCtx))
    return of.BoolResolutionDetail{
        Value: inner.Value,
        ProviderResolutionDetail: of.ProviderResolutionDetail{
            Reason: of.Reason(inner.Reason),
        },
    }
}

// Repeat for String / Int / Float / Object methods, and implement
// Metadata() and Hooks() as the SDK contract requires.
```

`toLocalCtx` is two lines: copy each context attribute as a string into an `openfeature.EvaluationContext`.

## Things this adapter does NOT do

- Track per-user evaluation history (FastConf rules are stateless).
- Talk to a remote OpenFeature service (the rules live in your YAML / overlays).
- Implement OpenFeature `Hook` semantics — that's the SDK's job.
