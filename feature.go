package fastconf

import (
	imanager "github.com/fastabc/fastconf/internal/manager"
	"github.com/fastabc/fastconf/pkg/feature"
)

type EvalContext = feature.EvalContext
type FeatureRule = feature.Rule

func WithFeatureRules[T any](extract func(*T) map[string]feature.Rule) Option {
	if extract == nil {
		return func(*options) {}
	}
	return func(o *options) {
		o.FeatureExtract = func(state any) map[string]feature.Rule {
			ptr, ok := state.(*T)
			if !ok || ptr == nil {
				return nil
			}
			return extract(ptr)
		}
	}
}

func Eval[T any, V any](m *Manager[T], key string, ctx feature.EvalContext, def V) V {
	if m == nil || m.inner == nil {
		return def
	}
	return imanager.Eval[T, V](m.inner, key, ctx, def)
}
