// Package env provides configuration layers from process environment variables.
package env

import (
	"context"
	"os"
	"strings"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// EnvKeyReplacer transforms the post-prefix portion of an env var name into a dotted-path string.
// An empty result means "skip this key".
type EnvKeyReplacer interface {
	Replace(s string) string
}

// EnvKeyReplacerFunc is an EnvKeyReplacer adapter for plain funcs.
type EnvKeyReplacerFunc func(string) string

// Replace implements EnvKeyReplacer.
func (f EnvKeyReplacerFunc) Replace(s string) string { return f(s) }

// DotReplacer lowercases keys and collapses runs of underscores or dots into one dot, stripping
// leading and trailing separators.
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

// DoubleUnderscoreReplacer lowercases keys and splits only on double underscores, preserving
// single underscores within each segment.
var DoubleUnderscoreReplacer EnvKeyReplacer = EnvKeyReplacerFunc(func(s string) string {
	parts := strings.Split(s, "__")
	for i, part := range parts {
		parts[i] = strings.ToLower(part)
	}
	return strings.Join(parts, ".")
})

// NewEnvReplacer is a thin shortcut for NewEnv(prefix).WithReplacer(replacer). A nil replacer
// means DotReplacer (the EnvProvider default).
func NewEnvReplacer(prefix string, replacer EnvKeyReplacer) *EnvProvider {
	return NewEnv(prefix).WithReplacer(replacer)
}

// EnvProvider maps matching environment keys through its EnvKeyReplacer. Values remain strings
// unless WithCoerce is enabled; typed hooks can convert them during decoding. At grafts the tree
// below a dotted path. For variable expansion inside values, register transform.EnvSubst
// separately.
type EnvProvider struct {
	prefix   string
	priority int
	coerce   bool
	replacer EnvKeyReplacer
	root     []string // optional graft path; empty = root of the merged map
	getenv   func() []string
}

// NewEnv selects matching variables with PriorityEnv and DotReplacer. Values remain strings unless
// WithCoerce(true) is set.
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

// WithCoerce toggles eager value coercion. When true, values are converted to bool / int64 /
// float64 / string at Load time (opt-in scalar coercion). When false (default), values stay as
// strings and the typed decoder chain converts them to the destination field type.
func (p *EnvProvider) WithCoerce(on bool) *EnvProvider { p.coerce = on; return p }

// WithReplacer swaps the key-conversion strategy. Passing nil restores the default DotReplacer.
func (p *EnvProvider) WithReplacer(r EnvKeyReplacer) *EnvProvider {
	if r == nil {
		r = DotReplacer
	}
	p.replacer = r
	return p
}

// At grafts the loaded tree under a dotted path; empty keeps it at the root.
func (p *EnvProvider) At(path string) *EnvProvider {
	p.root = confmap.Split(path)
	return p
}

// withEnviron is for tests.
func (p *EnvProvider) withEnviron(fn func() []string) *EnvProvider { p.getenv = fn; return p }

// Name implements Provider.
func (p *EnvProvider) Name() string { return "env:" + p.prefix }

// Describe implements contracts.Describer.
func (p *EnvProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider.
func (p *EnvProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

func (p *EnvProvider) loadMap(_ context.Context) (map[string]any, error) {
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
		confmap.Set(inner, strings.Split(dotted, "."), providerutil.MaybeCoerce(v, p.coerce))
	}
	return providerutil.GraftAt(inner, p.root), nil
}

// Watch implements Provider. Env is not watched.
func (p *EnvProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}
