package dotenv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
	envprovider "github.com/fastabc/fastconf/providers/env"
)

// DotEnvProvider reads .env files using env.EnvKeyReplacer (env.DotReplacer by default).
// Process environment keys suppress matching .env keys even when explicitly empty;
// register env.NewEnv as well to load those process values.
// PriorityDotEnv puts this layer below other built-in providers. No os.Setenv
// calls are made, preserving tenant and test isolation.
//
// Later files override earlier ones. Values support unquoted strings, double
// quotes with escapes, and literal single quotes. Blank lines and full-line
// comments are skipped; optional export prefixes are accepted. Inline # remains
// part of a value. Variable references are literal; use transform.EnvSubst
// for interpolation after merging.
type DotEnvProvider struct {
	prefix   string
	paths    []string
	priority int
	coerce   bool
	replacer envprovider.EnvKeyReplacer
	root     []string
	lookup   EnvLookup
}

// EnvLookup reports both an env var's value and whether it exists. The boolean is intentionally
// part of the contract so explicit empty values still win over .env fallbacks.
type EnvLookup func(string) (string, bool)

// NewDotEnv loads paths in order, stripping prefix and converting keys with env.DotReplacer. Values
// stay strings unless WithCoerce(true) is set.
func NewDotEnv(prefix string, paths ...string) *DotEnvProvider {
	return &DotEnvProvider{
		prefix:   prefix,
		paths:    paths,
		priority: contracts.PriorityDotEnv,
		replacer: envprovider.DotReplacer,
		lookup:   os.LookupEnv,
	}
}

// WithPriority overrides the default priority.
func (p *DotEnvProvider) WithPriority(prio int) *DotEnvProvider {
	p.priority = prio
	return p
}

// WithCoerce toggles eager value coercion. See env.EnvProvider.WithCoerce.
func (p *DotEnvProvider) WithCoerce(on bool) *DotEnvProvider {
	p.coerce = on
	return p
}

// WithReplacer swaps the key-conversion strategy. Passing nil restores env.DotReplacer.
// See env.EnvProvider.WithReplacer.
func (p *DotEnvProvider) WithReplacer(r envprovider.EnvKeyReplacer) *DotEnvProvider {
	if r == nil {
		r = envprovider.DotReplacer
	}
	p.replacer = r
	return p
}

// At grafts the loaded tree under the given dotted path instead of the root of the merged
// configuration. See env.EnvProvider.At.
func (p *DotEnvProvider) At(path string) *DotEnvProvider {
	p.root = confmap.Split(path)
	return p
}

// WithLookup swaps the env lookup used to decide whether a process env value suppresses a .env
// fallback. Passing nil restores os.LookupEnv.
func (p *DotEnvProvider) WithLookup(fn EnvLookup) *DotEnvProvider {
	if fn == nil {
		fn = os.LookupEnv
	}
	p.lookup = fn
	return p
}

// Name implements Provider.
func (p *DotEnvProvider) Name() string { return "dotenv:" + strings.Join(p.paths, ",") }

// Describe implements contracts.Describer.
func (p *DotEnvProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider.
func (p *DotEnvProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

func (p *DotEnvProvider) loadMap(_ context.Context) (map[string]any, error) {
	inner := map[string]any{}
	for _, path := range p.paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("dotenv provider: read %q: %w", path, err)
		}
		pairs, err := parseDotEnv(data)
		if err != nil {
			return nil, fmt.Errorf("dotenv provider: parse %q: %w", path, err)
		}
		for k, v := range pairs {
			// Actual env vars take precedence: skip keys already present in
			// the process environment, even when their value is explicitly
			// empty. Check k directly — it is the full raw key from the .env
			// file (e.g. APP_PORT) and is not yet prefix-stripped.
			if p.lookup != nil {
				if _, ok := p.lookup(k); ok {
					continue
				}
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
	}
	return providerutil.GraftAt(inner, p.root), nil
}

// Watch implements Provider. Dotenv files are not watched.
func (p *DotEnvProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}

// parseDotEnv parses .env file bytes and returns KEY → raw-string pairs. Keys retain their
// original case; stripping and lowercasing is the caller's responsibility (same as EnvProvider).
func parseDotEnv(data []byte) (map[string]string, error) {
	out := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineno := 0
	for scanner.Scan() {
		lineno++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix.
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			// Lines without '=' are silently ignored (e.g. bare "export KEY").
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		raw := line[eq+1:]
		val, err := parseValue(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineno, err)
		}
		out[key] = val
	}
	return out, scanner.Err()
}

// parseValue handles unquoted, single-quoted, and double-quoted values.
func parseValue(s string) (string, error) {
	if len(s) == 0 {
		return "", nil
	}
	switch s[0] {
	case '\'':
		// Single-quoted: no escape processing; must be closed.
		end := strings.Index(s[1:], "'")
		if end < 0 {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return s[1 : end+1], nil
	case '"':
		// Double-quoted: process backslash escapes.
		return parseDoubleQuoted(s[1:])
	default:
		// Unquoted: trim trailing whitespace; inline # not stripped.
		return strings.TrimRight(s, " \t"), nil
	}
}

func parseDoubleQuoted(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			return b.String(), nil
		}
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("unterminated double-quoted value")
}

// AutoDotEnvPaths returns configDir/.env followed by .env; missing files are skipped.
func AutoDotEnvPaths(configDir string) []string {
	cwd, _ := os.Getwd()
	candidates := make([]string, 0, 3)
	if configDir != "" {
		candidates = append(candidates, filepath.Join(configDir, ".env"))
	}
	if cwd != "" && cwd != configDir {
		candidates = append(candidates, filepath.Join(cwd, ".env"))
	}
	return candidates
}
