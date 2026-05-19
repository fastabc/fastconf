package options

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/diffreport"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/obs"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/registry"
	"github.com/fastabc/fastconf/internal/secret"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/discovery"
	"github.com/fastabc/fastconf/pkg/feature"
	"github.com/fastabc/fastconf/pkg/flog"
	"github.com/fastabc/fastconf/pkg/provider"
	"github.com/fastabc/fastconf/pkg/transform"
	"github.com/fastabc/fastconf/policy"
)

type Option func(*Options)

type CodecBridge uint8

const (
	BridgeJSON CodecBridge = iota
	BridgeYAML
)

type OverlayAxis = discovery.AxisSpec
type Transformer = transform.Transformer

type MigrationApplier interface {
	Migrate(map[string]any) error
}

type MigrationFunc func(map[string]any) error

func (fn MigrationFunc) Migrate(root map[string]any) error { return fn(root) }

type Defaulter interface {
	Defaults()
}

type ValidatorEntry struct {
	Fn func(any) error
}

type PendingByName struct {
	Name string
	Cfg  map[string]any
}

type ProviderFactory = registry.Factory
type ProviderRegistry = registry.Registry

const DiffReporterQueueCap = 64

const (
	DefaultDir               = "conf.d"
	DefaultProfileEnv        = "APP_PROFILE"
	DefaultCoalesceQuiet     = coalesce.DefaultQuiet
	DefaultCoalesceMaxLag    = coalesce.DefaultMaxLag
	DefaultCoalesceSwapHint  = coalesce.DefaultSwapHint
	DefaultSidecarHistoryCap = 16
)

type Options struct {
	Dir         string
	FS          fs.FS
	Profile     string
	Profiles    []string
	ProfileExpr string
	ProfileEnv  string
	DefaultProf string
	Strict      bool
	Logger      *slog.Logger
	Log         *flog.Logger
	Providers   []contracts.Provider

	Watch       bool
	Coalesce    coalesce.Options
	WatchPaths  []string
	OverlayAxes []discovery.AxisSpec

	Metrics        obs.MetricsBridge
	Validators     []ValidatorEntry
	Transformers   []Transformer
	Provenance     provenance.Level
	HistoryCap     int
	SecretRedactor secret.Redactor
	StructDefaults func(any) error
	DefaulterFunc  func(any)
	CodecBridge    CodecBridge
	MigrationRun   MigrationApplier
	AuditSinks     []obs.AuditSink
	Tracer         obs.Tracer
	Policies       []policy.AnyPolicy
	DeferredErrs   []error
	RawMapHook     func(map[string]any)
	SecretResolver secret.Resolver

	FeatureExtract func(any) map[string]feature.Rule

	Generators []contracts.Generator

	TypedHooks    []decoder.TypedHook
	TypedHooksOff bool

	MergeKeys map[string]string

	DiffReporters        []diffreport.Reporter[istate.DiffEvent]
	DiffReporterQueueCap int

	DotEnvAutoPrefixes []string
	Tenant             string

	ProviderRegistry *registry.Registry
	PendingByName    []PendingByName
}

func Default() Options {
	base := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return Options{
		Dir:      DefaultDir,
		Strict:   false,
		Logger:   base,
		Log:      flog.New(base),
		Coalesce: coalesce.ProfileK8s.Apply(),
		Metrics:  obs.NewMetricsBridge(obs.NoopMetrics{}),
		Tracer:   obs.NoopTracer{},
	}
}

func (o *Options) RefreshLog() {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	o.Log = flog.New(o.Logger)
}

func (o *Options) ApplyDeferredDotEnvAuto() {
	for _, prefix := range o.DotEnvAutoPrefixes {
		paths := provider.AutoDotEnvPaths(o.Dir)
		if len(paths) > 0 {
			o.Providers = append(o.Providers, provider.NewDotEnv(prefix, paths...))
		}
	}
	o.DotEnvAutoPrefixes = nil
}

func (o *Options) EffectiveProfile(metaProfileEnv, metaDefault string) string {
	if o.Profile != "" {
		return o.Profile
	}
	env := o.ProfileEnv
	if env == "" {
		env = metaProfileEnv
	}
	if env == "" {
		env = DefaultProfileEnv
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	if o.DefaultProf != "" {
		return o.DefaultProf
	}
	return metaDefault
}

func (o *Options) ResolveProvidersByName() {
	for _, p := range o.PendingByName {
		f, ok := o.lookupProviderFactory(p.Name)
		if !ok {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: provider factory %q not registered (have: %v)",
					fcerr.ErrFastConf, p.Name, o.knownProviderNames()))
			continue
		}
		pr, err := f(p.Cfg)
		if err != nil {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: provider %q: %v", fcerr.ErrFastConf, p.Name, err))
			continue
		}
		o.Providers = append(o.Providers, pr)
	}
	o.PendingByName = nil
}

func (o *Options) lookupProviderFactory(name string) (ProviderFactory, bool) {
	if o.ProviderRegistry != nil {
		if f, ok := o.ProviderRegistry.Lookup(name); ok {
			return f, true
		}
	}
	return registry.Default.Lookup(name)
}

func (o *Options) knownProviderNames() []string {
	seen := map[string]struct{}{}
	for _, n := range registry.Default.Names() {
		seen[n] = struct{}{}
	}
	if o.ProviderRegistry != nil {
		for _, n := range o.ProviderRegistry.Names() {
			seen[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func WithMergeKeys(o *Options, keys map[string]string) {
	if o.MergeKeys == nil {
		o.MergeKeys = map[string]string{}
	}
	maps.Copy(o.MergeKeys, keys)
}

type priorityOverride struct {
	contracts.Provider
	priority int
}

func (p *priorityOverride) Priority() int { return p.priority }

func (p *priorityOverride) WatchPaths() []string {
	if wp, ok := p.Provider.(contracts.WatchPathProvider); ok {
		return wp.WatchPaths()
	}
	return nil
}

func (p *priorityOverride) WatchFrom(ctx context.Context, lastRev string) (<-chan contracts.Event, error) {
	if r, ok := p.Provider.(contracts.Resumable); ok {
		return r.WatchFrom(ctx, lastRev)
	}
	return nil, contracts.ErrResumeUnsupported
}

func WrapWithPriority(p contracts.Provider, prio int) contracts.Provider {
	return &priorityOverride{Provider: p, priority: prio}
}

func TrimProfiles(dst []string, vals []string) []string {
	for _, x := range vals {
		x = strings.TrimSpace(x)
		if x != "" {
			dst = append(dst, x)
		}
	}
	return dst
}
