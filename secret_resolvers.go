package fastconf

import (
	"context"
	"strings"
)

// WithSecretResolvers appends resolvers. The first recognizer for each leaf
// resolves it; nil entries are ignored. Resolved paths are masked in diagnostics.
func WithSecretResolvers(rs ...SecretResolver) Option {
	return func(o *options) {
		for _, r := range rs {
			if r != nil {
				o.SecretResolvers = append(o.SecretResolvers, r)
			}
		}
	}
}

// PrefixSecretResolver adapts a prefix and application-owned decrypter. Empty
// scheme/prefix or a nil resolve function is a programming error and panics.
func PrefixSecretResolver(scheme, prefix string, resolve func(context.Context, string) (string, error)) SecretResolver {
	if scheme == "" || prefix == "" || resolve == nil {
		panic("fastconf: invalid prefix secret resolver")
	}
	return SecretResolverFunc{
		RecognizeFn: func(v string) (SecretRef, bool) {
			body, ok := strings.CutPrefix(v, prefix)
			return SecretRef{Scheme: scheme, Body: body}, ok
		},
		ResolveFn: func(ctx context.Context, ref SecretRef) (string, error) { return resolve(ctx, ref.Body) },
	}
}
