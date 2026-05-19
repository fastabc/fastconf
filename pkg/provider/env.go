// Package provider abstracts external configuration sources (env, CLI, KV,
// Vault, ...). Providers are second-class citizens of the reload pipeline:
// their output is merged into the same map[string]any that file discovery
// produces, after which the merged document is decoded into the user's
// strongly-typed *T.
//
// The Provider/Event interface itself lives in fastconf/contracts; this
// package consumes that interface via re-exports and ships the built-in
// Env / CLI / File / Labels / RoutingLabels / DotEnv implementations.
package provider

import (
	"context"
	"os"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
	"github.com/fastabc/fastconf/pkg/typed"
)

// =====================================================================
//   ENV KEY REPLACERS
// =====================================================================
//
// The default DotReplacer (single "_" → ".") matches Viper and Spring
// Boot's relaxed binding — the broadly expected behavior across the Go
// ecosystem. DoubleUnderscoreReplacer is the alternate convention that
// preserves single "_" as part of the key and uses "__" as the level
// separator. Custom replacers can implement any naming scheme.

// EnvKeyReplacer transforms the post-prefix portion of an env var name
// into a dotted-path string. An empty result means "skip this key".
type EnvKeyReplacer interface {
	Replace(s string) string
}

// EnvKeyReplacerFunc is an EnvKeyReplacer adapter for plain funcs.
type EnvKeyReplacerFunc func(string) string

// Replace implements EnvKeyReplacer.
func (f EnvKeyReplacerFunc) Replace(s string) string { return f(s) }

// DotReplacer is the canonical "single underscore → dot" replacer,
// matching Viper's SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
// and Spring Boot relaxed binding. Runs of underscores collapse to a
// single dot so SCREAMING__SNAKE keys produce sane nested paths even
// when copy-pasted from a deployment that previously used the
// DoubleUnderscoreReplacer convention.
//
//	APP_DATABASE_DSN,  prefix="APP_" → "database.dsn"
//	APP_DATABASE__DSN, prefix="APP_" → "database.dsn"  (consecutive runs collapse)
var DotReplacer EnvKeyReplacer = EnvKeyReplacerFunc(func(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDot := true // suppress leading separator
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			c = '.'
		}
		if c == '.' {
			if prevDot {
				continue
			}
			prevDot = true
		} else {
			prevDot = false
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
		}
		b.WriteByte(c)
	}
	return strings.TrimSuffix(b.String(), ".")
})

// DoubleUnderscoreReplacer preserves single "_" as part of the key and
// uses "__" (double underscore) as the nesting separator. Use when env
// keys legitimately carry underscores that must survive flattening
// (e.g. SCREAMING_SNAKE feature flag names).
//
//	APP_FEATURE_FLAGS,  prefix="APP_" → "feature_flags"  (one level)
//	APP_DATABASE__POOL, prefix="APP_" → "database.pool"   (two levels)
var DoubleUnderscoreReplacer EnvKeyReplacer = EnvKeyReplacerFunc(func(s string) string {
	parts := strings.Split(s, "__")
	for i, part := range parts {
		parts[i] = strings.ToLower(part)
	}
	return strings.Join(parts, ".")
})

// NewEnvReplacer is a thin shortcut for
// NewEnv(prefix).WithReplacer(replacer). A nil replacer means
// DotReplacer (the EnvProvider default).
func NewEnvReplacer(prefix string, replacer EnvKeyReplacer) *EnvProvider {
	return NewEnv(prefix).WithReplacer(replacer)
}

// =====================================================================
//   ENV PROVIDER
// =====================================================================

// EnvProvider reads process environment variables matching a prefix and
// translates them into a nested map. Key conversion is delegated to an
// EnvKeyReplacer strategy:
//
//   - DotReplacer (default) — Viper / Spring Boot relaxed-binding style:
//     each "_" becomes a level. APP_DATABASE_DSN with prefix="APP_"
//     produces {"database":{"dsn":"..."}}.
//   - DoubleUnderscoreReplacer — preserves single "_" as part of the
//     key, splits only on "__". APP_FEATURE_FLAGS stays a single key
//     "feature_flags"; APP_DATABASE__POOL produces {"database":{"pool":"20"}}.
//     Use this when keys carry literal underscores that must be retained.
//   - A custom EnvKeyReplacer for bespoke conventions.
//
// Values are kept verbatim as strings by default; the typed-decode step
// (see pkg/decoder.StringPrimitiveHook in DefaultTypedHooks) converts
// them to the destination field type during *T decode. Set Coerce=true
// via WithCoerce to opt into eager bool/int64/float64 coercion at load
// time — useful when the merged map is consumed without the typed-decode
// hook chain.
//
// Use At(path) to graft the loaded tree under a configurable root path
// (e.g. "config.runtime") instead of polluting the root of the merged
// configuration.
//
// EnvProvider does NOT expand ${VAR} or $VAR references inside values.
// Add transform.EnvSubst (or transform.EnvSubstWith for a custom lookup)
// as a pipeline stage if you need that behavior; this is the equivalent of
// caarlos0/env's envExpand.
type EnvProvider struct {
	prefix   string
	priority int
	coerce   bool
	replacer EnvKeyReplacer
	root     []string // optional graft path; empty = root of the merged map
	getenv   func() []string
}

// NewEnv builds an EnvProvider with the given prefix (e.g. "APP_"),
// the default Env priority, and the DotReplacer (single "_" → ".") key
// strategy. Coerce defaults to false; call WithCoerce(true) to opt into
// eager bool/int/float coercion at Load time.
//
// Switch the key strategy with WithReplacer when the default does not
// match your deployment's env-naming convention:
//
//	// Spring-style relaxed binding (default):
//	provider.NewEnv("APP_")
//
//	// Preserve single "_" in keys; split only on "__":
//	provider.NewEnv("APP_").WithReplacer(provider.DoubleUnderscoreReplacer)
//
//	// Fully custom:
//	provider.NewEnv("APP_").WithReplacer(provider.EnvKeyReplacerFunc(...))
func NewEnv(prefix string) *EnvProvider {
	return &EnvProvider{
		prefix:   prefix,
		priority: contracts.PriorityEnv,
		replacer: DotReplacer,
		getenv:   os.Environ,
	}
}

// WithPriority overrides the default priority.
func (p *EnvProvider) WithPriority(prio int) *EnvProvider { p.priority = prio; return p }

// WithCoerce toggles eager value coercion. When true, values are
// converted to bool / int64 / float64 / string at Load time (opt-in
// scalar coercion). When false (default), values stay as strings and
// the typed decoder chain converts them to the destination field type.
func (p *EnvProvider) WithCoerce(on bool) *EnvProvider { p.coerce = on; return p }

// WithReplacer swaps the key-conversion strategy. Passing nil restores
// the default DotReplacer.
func (p *EnvProvider) WithReplacer(r EnvKeyReplacer) *EnvProvider {
	if r == nil {
		r = DotReplacer
	}
	p.replacer = r
	return p
}

// At grafts the loaded tree under the given dotted path instead of the
// root of the merged configuration. Useful for namespacing env-injected
// values without re-keying every env line:
//
//	provider.NewEnv("APP_").At("config.runtime")
//	// APP_DATABASE_DSN → {"config":{"runtime":{"database":{"dsn":"..."}}}}
//
// An empty path (default) keeps the legacy root-level behavior.
func (p *EnvProvider) At(path string) *EnvProvider {
	p.root = mappath.Split(path)
	return p
}

// withEnviron is for tests.
func (p *EnvProvider) withEnviron(fn func() []string) *EnvProvider { p.getenv = fn; return p }

// Name implements Provider.
func (p *EnvProvider) Name() string { return "env:" + p.prefix }

// Priority implements Provider.
func (p *EnvProvider) Priority() int { return p.priority }

// Load implements Provider.
func (p *EnvProvider) Load(_ context.Context) (map[string]any, error) {
	inner := map[string]any{}
	for _, kv := range p.getenv() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if p.prefix != "" && !strings.HasPrefix(k, p.prefix) {
			continue
		}
		k = strings.TrimPrefix(k, p.prefix)
		if k == "" {
			continue
		}
		dotted := p.replacer.Replace(k)
		if dotted == "" {
			continue
		}
		mappath.Set(inner, strings.Split(dotted, "."), maybeCoerce(v, p.coerce))
	}
	return graftAt(inner, p.root), nil
}

// Watch implements Provider. Env is not watched.
func (p *EnvProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }

// graftAt returns inner when root is empty; otherwise it wraps inner
// under root[0]/root[1]/... so callers can namespace the loaded tree.
func graftAt(inner map[string]any, root []string) map[string]any {
	if len(root) == 0 {
		return inner
	}
	out := map[string]any{}
	mappath.Set(out, root, inner)
	return out
}

// maybeCoerce returns s as-is when on==false; otherwise it converts to
// bool / int64 / float64 / string in that order.
func maybeCoerce(s string, on bool) any {
	if !on {
		return s
	}
	return coerce(s)
}

func coerce(s string) any {
	return typed.Coerce(s, typed.CoerceOptions{IgnoreCase: true})
}
