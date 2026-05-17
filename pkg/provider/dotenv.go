package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// DotEnvProvider reads one or more .env files and emits a nested map using
// the same key convention as EnvProvider: an optional prefix is stripped
// and the active EnvKeyReplacer (default DotReplacer, single "_" → ".")
// converts each post-prefix name into a dotted path. Use
// WithReplacer(DoubleUnderscoreReplacer) for the "single `_` is part of
// the key" convention. Actual process environment variables take
// precedence over .env values for the same key, matching the classic
// dotenv contract.
//
// Priority defaults to PriorityDotEnv (5), so all other built-in providers
// override dotenv values.
//
// Supported .env syntax:
//
//   - KEY=VALUE          (unquoted; trailing spaces trimmed)
//   - KEY="double quoted"  (supports \n \t \" \\ escapes)
//   - KEY='single quoted'  (no escapes; literal content)
//   - export KEY=VALUE   (leading "export" keyword stripped)
//   - # comment lines
//   - Blank lines are ignored.
type DotEnvProvider struct {
	prefix   string
	paths    []string
	priority int
	coerce   bool
	replacer EnvKeyReplacer
	root     []string
	getenv   func(string) string // injectable for tests
}

// NewDotEnv builds a DotEnvProvider that reads the given .env file paths.
// prefix follows the same convention as NewEnv: e.g. "APP_" so that
// APP_DATABASE_HOST=db yields {"database":{"host":"db"}} under the
// default DotReplacer.
//
// Values are kept verbatim as strings by default; call WithCoerce(true)
// to opt into bool/int/float coercion at load time (legacy behavior).
func NewDotEnv(prefix string, paths ...string) *DotEnvProvider {
	return &DotEnvProvider{
		prefix:   prefix,
		paths:    paths,
		priority: contracts.PriorityDotEnv,
		replacer: DotReplacer,
		getenv:   os.Getenv,
	}
}

// WithPriority overrides the default priority.
func (p *DotEnvProvider) WithPriority(prio int) *DotEnvProvider {
	p.priority = prio
	return p
}

// WithCoerce toggles eager value coercion. See EnvProvider.WithCoerce.
func (p *DotEnvProvider) WithCoerce(on bool) *DotEnvProvider {
	p.coerce = on
	return p
}

// WithReplacer swaps the key-conversion strategy. Passing nil restores
// the default DotReplacer. See EnvProvider.WithReplacer.
func (p *DotEnvProvider) WithReplacer(r EnvKeyReplacer) *DotEnvProvider {
	if r == nil {
		r = DotReplacer
	}
	p.replacer = r
	return p
}

// At grafts the loaded tree under the given dotted path instead of the
// root of the merged configuration. See EnvProvider.At.
func (p *DotEnvProvider) At(path string) *DotEnvProvider {
	p.root = mappath.Split(path)
	return p
}

// withGetenv is for tests.
func (p *DotEnvProvider) withGetenv(fn func(string) string) *DotEnvProvider {
	p.getenv = fn
	return p
}

// Name implements Provider.
func (p *DotEnvProvider) Name() string { return "dotenv:" + strings.Join(p.paths, ",") }

// Priority implements Provider.
func (p *DotEnvProvider) Priority() int { return p.priority }

// Load implements Provider.
func (p *DotEnvProvider) Load(_ context.Context) (map[string]any, error) {
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
			// Actual env vars take precedence: skip keys already set in the
			// process environment. Check k directly — it is the full raw key
			// from the .env file (e.g. APP_PORT) and is not yet prefix-stripped.
			if p.getenv != nil && p.getenv(k) != "" {
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
	}
	return graftAt(inner, p.root), nil
}

// Watch implements Provider. Dotenv files are not watched.
func (p *DotEnvProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }

// parseDotEnv parses .env file bytes and returns KEY → raw-string pairs.
// Keys retain their original case; stripping and lowercasing is the
// caller's responsibility (same as EnvProvider).
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

// AutoDotEnvPaths returns the default .env file search paths for
// WithDotEnvAuto: [configDir + "/.env", ".env"]. Missing files are skipped
// by DotEnvProvider.Load, so callers do not need to pre-check existence.
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
