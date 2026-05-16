package fastconf

// Feature flag / rollout evaluator surfaced on the Manager.
//
// The actual rule engine lives in pkg/feature (Rule / Target / Rollout /
// EvalContext). This file is the glue that lets a Manager[T] expose a
// strongly-typed "give me the value for this flag" entry point on top
// of the lock-free *State[T] snapshot.

import "github.com/fastabc/fastconf/pkg/feature"

// EvalContext re-exports pkg/feature.EvalContext so callers do not need
// to import a second package solely for the type.
type EvalContext = feature.EvalContext

// FeatureRule re-exports pkg/feature.Rule for the same reason.
type FeatureRule = feature.Rule

// WithFeatureRules attaches a per-reload rule extractor to the Manager.
// The extractor is invoked at the end of every successful reload to
// derive a map[string]feature.Rule from the freshly committed *T; the
// result is stamped onto the new State[T] so future Eval() calls are
// O(1) atomic loads.
//
// Pass a closure that pulls the rules table out of your config struct:
//
//	type AppConfig struct {
//	    Features map[string]feature.Rule `json:"features"`
//	}
//	mgr, _ := fastconf.New[AppConfig](ctx,
//	    fastconf.WithFeatureRules[AppConfig](func(c *AppConfig) map[string]feature.Rule {
//	        return c.Features
//	    }),
//	)
//
// Without WithFeatureRules, Manager.Eval always returns the supplied
// default.
func WithFeatureRules[T any](extract func(*T) map[string]feature.Rule) Option {
	if extract == nil {
		return func(*options) {}
	}
	return func(o *options) {
		o.featureExtract = func(state any) map[string]feature.Rule {
			ptr, ok := state.(*T)
			if !ok || ptr == nil {
				return nil
			}
			return extract(ptr)
		}
	}
}

// Eval looks up a feature rule by key against the live *State[T] feature
// table, evaluates it under ctx, and returns the rule value if it matches
// V (the typed default's type). Returns def in any of these cases:
//
//   - m or its current state is nil
//   - WithFeatureRules was never configured (no rule table)
//   - The rule for key is missing
//   - The rule value cannot be type-asserted to V
//
// Eval is zero-allocation on the hot path: one atomic snapshot load, one
// map lookup, optionally one deterministic hash/compare, then a typed
// return.
//
//	dark := fastconf.Eval[AppConfig, bool](mgr, "darkMode", flagCtx, false)
//
// For integrations that need the raw any-typed return (OpenFeature, etc.),
// call feature.Eval(state.FeatureRules(), key, ctx, def) directly.
func Eval[T any, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V {
	if m == nil {
		return def
	}
	s := m.state.Load()
	if s == nil {
		return def
	}
	raw := feature.Eval(s.features, key, ctx, def)
	if v, ok := raw.(V); ok {
		return v
	}
	return def
}
