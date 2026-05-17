package provider

// Env key-conversion strategies used by EnvProvider / DotEnvProvider.
//
// The default DotReplacer (single "_" → ".") matches Viper and Spring
// Boot's relaxed binding — the broadly expected behavior across the Go
// ecosystem. DoubleUnderscoreReplacer is the alternate convention that
// preserves single "_" as part of the key and uses "__" as the level
// separator. Custom replacers can implement any naming scheme.

import (
	"strings"
)

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
