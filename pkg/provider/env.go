package provider

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// EnvProvider reads process environment variables matching a prefix and
// translates them into a nested map. Conventions:
//
//   - Prefix is stripped.
//   - "__" (double underscore) introduces a nesting level. Single underscore is
//     preserved as part of the key (handles names like APP_FEATURE_FLAGS).
//   - Keys are lower-cased.
//   - Values are coerced to bool / int64 / float64 / string in that order.
//
// Example: APP_DATABASE__POOL=20 with prefix "APP_" produces
// {"database":{"pool":int64(20)}}.
type EnvProvider struct {
	prefix   string
	priority int
	getenv   func() []string // injectable for tests
}

// NewEnv builds an EnvProvider with the given prefix (e.g. "APP_") and the
// default Env priority.
func NewEnv(prefix string) *EnvProvider {
	return &EnvProvider{prefix: prefix, priority: contracts.PriorityEnv, getenv: os.Environ}
}

// WithPriority overrides the default priority.
func (p *EnvProvider) WithPriority(prio int) *EnvProvider { p.priority = prio; return p }

// withEnviron is for tests.
func (p *EnvProvider) withEnviron(fn func() []string) *EnvProvider { p.getenv = fn; return p }

// Name implements Provider.
func (p *EnvProvider) Name() string { return "env:" + p.prefix }

// Priority implements Provider.
func (p *EnvProvider) Priority() int { return p.priority }

// Load implements Provider.
func (p *EnvProvider) Load(_ context.Context) (map[string]any, error) {
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
		parts := strings.Split(k, "__")
		for i, part := range parts {
			parts[i] = strings.ToLower(part)
		}
		mappath.Set(out, parts, coerce(v))
	}
	return out, nil
}

// Watch implements Provider. Env is not watched.
func (p *EnvProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }

func coerce(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
