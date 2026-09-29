// Package cli centralises the FastConf command-line flag set so every
// cmd/* binary registers -dir / -profile / -strict / -watch with
// identical defaults and semantics, and constructs the Manager via a
// single canonical path.
//
// Sub-commands embed Flags into their flag.FlagSet, call RegisterFlags,
// then hand the populated value to LoadConfig[T] together with any
// command-specific Option overrides. Adding a new CLI binary becomes
// "wire Flags + call LoadConfig + run business logic" with no
// boilerplate.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/dotenv"
	"github.com/fastabc/fastconf/providers/env"
	"github.com/fastabc/fastconf/providers/source"
)

// Flags is the canonical FastConf CLI flag set shared by fastconfd and
// fastconfctl (and any future binary). Each sub-command embeds one of
// these inside its own flag.FlagSet via RegisterFlags.
type Flags struct {
	Dir       string
	Profile   string
	Strict    bool
	Watch     bool
	Providers ProviderFlags
}

// ProviderFlags is a repeatable "-provider name=value" flag. Value may
// be JSON (decoded into a map) or a plain string (wrapped as
// {"value": s}). Apply converts the collected specs into
// fastconf.Options on demand.
type ProviderFlags []string

// String implements flag.Value.
func (p *ProviderFlags) String() string { return fmt.Sprintf("%v", *p) }

// Set implements flag.Value.
func (p *ProviderFlags) Set(v string) error {
	*p = append(*p, v)
	return nil
}

// RegisterFlags registers the shared FastConf flags on the given
// FlagSet. Defaults are pulled from the fastconf package so any future
// change to e.g. DefaultDir propagates to every CLI binary automatically.
func RegisterFlags(fs *flag.FlagSet, f *Flags) {
	fs.StringVar(&f.Dir, "dir", fastconf.DefaultDir, "configuration root directory")
	fs.StringVar(&f.Profile, "profile", "", "overlay profile (empty = base only or via $APP_PROFILE)")
	fs.BoolVar(&f.Strict, "strict", false, "strict overlay/merge: unknown file extensions and merge type conflicts fail (does not reject misspelled config keys)")
	fs.BoolVar(&f.Watch, "watch", false, "enable fsnotify file-system watcher")
	fs.Var(&f.Providers, "provider", "name=value provider spec (repeatable; value may be JSON)")
}

// ChangedValues visits only flags explicitly set by the user and lets the
// caller project them into a CLI-provider map. It intentionally skips parser
// defaults so lower-priority config remains authoritative unless the user
// typed an override.
//
// When build is nil, ChangedValues records a flat name → string-value map.
func ChangedValues(fs *flag.FlagSet, build func(name, value string, out map[string]any) error) (map[string]any, error) {
	out := map[string]any{}
	var firstErr error
	fs.Visit(func(f *flag.Flag) {
		if firstErr != nil {
			return
		}
		if build == nil {
			out[f.Name] = f.Value.String()
			return
		}
		firstErr = build(f.Name, f.Value.String(), out)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// LoadConfig builds a fastconf.Manager[T] from a populated Flags value
// plus any extra Option overrides. It is the canonical Manager
// constructor for CLI binaries; -dir / -profile / -strict / -watch
// behaviour stays consistent across fastconfd and fastconfctl.
func LoadConfig[T any](ctx context.Context, f Flags, extra ...fastconf.Option) (*fastconf.Manager[T], error) {
	opts, err := f.Options(extra...)
	if err != nil {
		return nil, err
	}
	mgr, err := fastconf.New[T](ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cli: load config: %w", err)
	}
	return mgr, nil
}

// Options assembles the fastconf options selected by f, followed by extra.
func (f Flags) Options(extra ...fastconf.Option) ([]fastconf.Option, error) {
	opts := []fastconf.Option{
		fastconf.WithDir(f.Dir),
		fastconf.WithStrictMerge(f.Strict),
	}
	if f.Watch {
		opts = append(opts, fastconf.WithWatch(fastconf.Watch{}))
	}
	if f.Profile != "" {
		opts = append(opts, fastconf.WithProfile(fastconf.Profile{Names: strings.Split(f.Profile, ",")}))
	}
	if err := f.Providers.Apply(&opts); err != nil {
		return nil, err
	}
	return append(opts, extra...), nil
}

// ProviderFactory builds a provider from a "-provider name=value" spec.
// cfg is the decoded JSON value, or {"value": s} for a plain string.
type ProviderFactory func(cfg map[string]any) (contracts.Provider, error)

// Providers maps "-provider" names to factories. Binaries may add entries
// before calling LoadConfig.
var Providers = map[string]ProviderFactory{
	"env": func(cfg map[string]any) (contracts.Provider, error) {
		prefix, _ := cfg["value"].(string)
		return env.NewEnv(prefix), nil
	},
	"dotenv": func(cfg map[string]any) (contracts.Provider, error) {
		prefix, _ := cfg["prefix"].(string)
		var paths []string
		if p, ok := cfg["value"].(string); ok && p != "" {
			paths = append(paths, p)
		}
		return dotenv.NewDotEnv(prefix, paths...), nil
	},
	"file": func(cfg map[string]any) (contracts.Provider, error) {
		path, _ := cfg["value"].(string)
		if path == "" {
			return nil, errors.New("file provider: path required")
		}
		return source.NewFile(path), nil
	},
}

// Apply converts the parsed provider specs into fastconf.Options appended
// onto opts. Each spec is "name=value"; if value is valid JSON it becomes
// the provider config map, otherwise it is wrapped as {"value": s}. The
// name selects a factory from Providers.
func (p ProviderFlags) Apply(opts *[]fastconf.Option) error {
	for _, spec := range p {
		name, cfg, err := parseProviderSpec(spec)
		if err != nil {
			return err
		}
		f, ok := Providers[name]
		if !ok {
			return fmt.Errorf("provider %q: unknown (have %s)", name, strings.Join(slices.Sorted(maps.Keys(Providers)), ", "))
		}
		pr, err := f(cfg)
		if err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
		}
		*opts = append(*opts, fastconf.WithProvider(pr))
	}
	return nil
}

func parseProviderSpec(spec string) (string, map[string]any, error) {
	name, val, hasVal := strings.Cut(spec, "=")
	if name == "" {
		return "", nil, fmt.Errorf("provider spec %q: name must not be empty", spec)
	}
	if !hasVal || val == "" {
		return name, map[string]any{}, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(val), &cfg); err != nil {
		cfg = map[string]any{"value": val}
	}
	return name, cfg, nil
}
