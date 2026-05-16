package provider

// Phase 131 (SPEC-131) — Env key replacer + WithEnvAutoBind.
//
// The default EnvProvider splits names on "__" (double underscore) to
// introduce nesting levels. That works fine for systems already using
// the Viper "_"-as-separator-with-replacer pattern, but not all
// deployments line up — some teams want "APP_DATABASE_DSN" mapped to
// "database.dsn" without re-keying every env line.
//
// EnvKeyReplacer lets the user inject a fully custom env-name → dotted-
// path rewriter; NewEnvReplacerProvider wraps EnvProvider so the
// resulting provider still implements contracts.Provider but routes
// each "PREFIX_X_Y_Z" through Replacer.Replace(remainder) before
// building the nested map.

import (
	"context"
	"os"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// EnvKeyReplacer transforms the post-prefix portion of an env var name
// into a dotted-path string. Empty result means "skip this key".
type EnvKeyReplacer interface {
	Replace(s string) string
}

// EnvKeyReplacerFunc is an EnvKeyReplacer adapter for plain funcs.
type EnvKeyReplacerFunc func(string) string

// Replace implements EnvKeyReplacer.
func (f EnvKeyReplacerFunc) Replace(s string) string { return f(s) }

// DotReplacer is the canonical "single underscore → dot" replacer
// matching Viper's SetEnvKeyReplacer(strings.NewReplacer(".", "_")).
//
//	APP_DATABASE_DSN, prefix="APP_" → "database.dsn"
var DotReplacer EnvKeyReplacer = EnvKeyReplacerFunc(func(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", "."))
})

// ReplacerProvider is an EnvProvider variant that routes the post-prefix
// portion of each env name through Replacer before building the nested
// map. Value coercion (bool / int / float / string) is identical to
// EnvProvider.
type ReplacerProvider struct {
	prefix   string
	priority int
	replacer EnvKeyReplacer
	getenv   func() []string
}

// NewEnvReplacer builds a ReplacerProvider with the given prefix and
// replacer. If replacer is nil, DotReplacer is used.
func NewEnvReplacer(prefix string, replacer EnvKeyReplacer) *ReplacerProvider {
	if replacer == nil {
		replacer = DotReplacer
	}
	return &ReplacerProvider{prefix: prefix, priority: contracts.PriorityEnv, replacer: replacer, getenv: os.Environ}
}

// WithPriority overrides the default priority.
func (p *ReplacerProvider) WithPriority(prio int) *ReplacerProvider {
	p.priority = prio
	return p
}

// Name implements contracts.Provider.
func (p *ReplacerProvider) Name() string { return "env-replacer:" + p.prefix }

// Priority implements contracts.Provider.
func (p *ReplacerProvider) Priority() int { return p.priority }

// Load implements contracts.Provider.
func (p *ReplacerProvider) Load(_ context.Context) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range p.getenv() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
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
		mappath.Set(out, strings.Split(dotted, "."), coerce(v))
	}
	return out, nil
}

// Watch implements contracts.Provider. Env is not watched.
func (p *ReplacerProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
