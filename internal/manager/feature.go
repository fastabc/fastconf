package manager

import "github.com/fastabc/fastconf/feature"

func Eval[T any, V any](m *M[T], key string, ctx feature.EvalContext, def V) V {
	if m == nil {
		return def
	}
	s := m.state.Load()
	if s == nil {
		return def
	}
	// Eval is read-only over the rules; use the non-cloning ref accessor
	// so this request-path call stays allocation-free.
	raw := feature.Eval(s.FeatureRulesRef(), key, ctx, def)
	if v, ok := raw.(V); ok {
		return v
	}
	return def
}
