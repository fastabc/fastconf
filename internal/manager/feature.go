package manager

import "github.com/fastabc/fastconf/pkg/feature"

func Eval[T any, V any](m *M[T], key string, ctx feature.EvalContext, def V) V {
	if m == nil {
		return def
	}
	s := m.state.Load()
	if s == nil {
		return def
	}
	raw := feature.Eval(s.FeatureRules(), key, ctx, def)
	if v, ok := raw.(V); ok {
		return v
	}
	return def
}
