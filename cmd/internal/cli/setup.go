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
	"flag"
	"fmt"
	"strings"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/provider"
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
	fs.BoolVar(&f.Strict, "strict", false, "strict mode (unknown keys fail)")
	fs.BoolVar(&f.Watch, "watch", false, "enable fsnotify file-system watcher")
	fs.Var(&f.Providers, "provider", "name=value provider spec (repeatable; value may be JSON)")
}

// LoadConfig builds a fastconf.Manager[T] from a populated Flags value
// plus any extra Option overrides. It is the canonical Manager
// constructor for CLI binaries; -dir / -profile / -strict / -watch
// behaviour stays consistent across fastconfd and fastconfctl.
func LoadConfig[T any](ctx context.Context, f Flags, extra ...fastconf.Option) (*fastconf.Manager[T], error) {
	opts := []fastconf.Option{
		fastconf.WithDir(f.Dir),
		fastconf.WithStrict(f.Strict),
		fastconf.WithWatch(f.Watch),
	}
	if f.Profile != "" {
		opts = append(opts, fastconf.WithProfile(f.Profile))
	}
	if err := f.Providers.Apply(&opts); err != nil {
		return nil, err
	}
	opts = append(opts, extra...)
	mgr, err := fastconf.New[T](ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cli: load config: %w", err)
	}
	return mgr, nil
}

// Apply converts the parsed provider specs into fastconf.Options
// appended onto opts. Each spec is "name=value"; if value is valid JSON
// it becomes the provider config map, otherwise it is wrapped as
// {"value": s}. The "env" name is special-cased to a plain Env provider
// because it predates the registry-based WithProviderByName.
func (p ProviderFlags) Apply(opts *[]fastconf.Option) error {
	for _, spec := range p {
		name, cfg, err := parseProviderSpec(spec)
		if err != nil {
			return err
		}
		switch name {
		case "env":
			prefix, _ := cfg["value"].(string)
			*opts = append(*opts, fastconf.WithProvider(provider.NewEnv(prefix)))
		default:
			*opts = append(*opts, fastconf.WithProviderByName(name, cfg))
		}
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
