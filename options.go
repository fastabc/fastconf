package fastconf

import (
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/fcerr"
	iopts "github.com/fastabc/fastconf/internal/options"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/discovery"
	"github.com/fastabc/fastconf/policy"
)

type Option = iopts.Option
type options = iopts.Options

// CodecBridge selects the bytes-to-struct decoder used in the typed
// pipeline stage. BridgeJSON (default) round-trips through encoding/json
// so canonical-hash caching can reuse the marshalled bytes; BridgeYAML
// honours yaml struct tags directly. See WithCodecBridge for the user
// trap when *T has only yaml tags.
type CodecBridge uint8

const (
	BridgeJSON CodecBridge = iota
	BridgeYAML
)

// OverlayAxis describes one multi-axis overlay layer. Resolution order:
//
//  1. EnvVar present + non-empty       → use that value
//  2. EnvVar present + empty           → skip axis (operator opt-out)
//  3. EnvVar absent + DefaultFromHostname → fall back to os.Hostname()
//  4. otherwise                        → skip axis
//
// Priority should be a value in or above the contracts.BandExtraOverlay
// (3000) range to win over file-base / single-profile overlays. The
// Generator (7000) and Provider (8000) bands stay higher.
type OverlayAxis struct {
	Dir                 string
	EnvVar              string
	Priority            int
	DefaultFromHostname bool
}

// Transformer is the root-facade contract for the pre-decode raw-map
// transformation stage. Implementations get the merged
// map[string]any AFTER all source layers fold together and BEFORE the
// typed decoder runs, so they can rewrite keys, inject computed values,
// or normalise vendor-specific layouts without touching *T.
//
// The same shape is reused inside pkg/transform for the built-in
// transformers (Aliases, KeyMap, DropPrefix, EnvReplacer, …); third
// parties only need to satisfy this root interface.
type Transformer interface {
	Name() string
	Transform(map[string]any) error
}

// MigrationApplier is the root-facade contract for the version
// migration stage. Migrate is invoked once per reload on the merged raw
// map, before any transformer and before decoding into *T. Use
// MigrationFunc to adapt a plain `func(map[string]any) error` value.
type MigrationApplier interface {
	Migrate(map[string]any) error
}

// MigrationFunc adapts a plain function value to the MigrationApplier
// contract.
type MigrationFunc func(map[string]any) error

// Migrate implements MigrationApplier.
func (fn MigrationFunc) Migrate(root map[string]any) error { return fn(root) }

// WithCodecBridge selects the bytes-to-struct decoder for the typed
// stage. See [CodecBridge] for the BridgeJSON / BridgeYAML semantics.
//
// # Troubleshooting
//
// The default [BridgeJSON] round-trips through encoding/json so the
// canonical-hash cache can reuse the marshalled bytes. It honours
// `json:` and `fastconf:` struct tags only. Symptoms that indicate
// the default is mis-matched to your struct:
//
//   - snake_case keys in your YAML are silently dropped — the field is
//     left at its zero value (e.g. `db_pool: 50` ignored when *T only
//     declares `yaml:"db_pool"`). FastConf emits a one-time warn log
//     at New() to surface this; switch to [BridgeYAML] or add `json:`
//     tags.
//   - nested structs deserialize as nil — yaml's anchor / merge keys
//     are normalized to map[string]any by the decoder but json's
//     struct decoder reads field names, not tags, when no `json:` tag
//     is present.
//   - time.Time fields fail to parse — yaml's native time type encodes
//     as `2006-01-02T15:04:05Z` strings; the json bridge accepts those
//     only with a `time.Time`-aware typed hook.
//
// When in doubt, set [BridgeYAML] for YAML-tagged configs.
func WithCodecBridge(b CodecBridge) Option {
	return func(o *options) { o.CodecBridge = iopts.CodecBridge(b) }
}

func WithRawMapAccess(fn func(root map[string]any)) Option {
	if fn == nil {
		return func(*options) {}
	}
	return func(o *options) { o.RawMapHook = fn }
}

func WithDir(dir string) Option     { return func(o *options) { o.Dir = dir } }
func WithFS(f fs.FS) Option         { return func(o *options) { o.FS = f } }
func WithStrict(strict bool) Option { return func(o *options) { o.Strict = strict } }

// WithLogger overrides the default slog logger. Passing nil records a
// deferred error so a misconfigured logger fails loudly at New(), rather
// than silently routing every log line into the default backend.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l == nil {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithLogger(nil)", fcerr.ErrFastConf))
			return
		}
		o.Logger = l
	}
}

// CoalesceOptions tunes the file-watcher event coalescer. All three
// fields are optional — zero means "use the default for that field".
// See DefaultCoalesceQuiet / DefaultCoalesceMaxLag / DefaultCoalesceSwapHint.
type CoalesceOptions struct {
	// Quiet is the no-event silence window after which a burst of
	// fsnotify events is delivered as a single reload.
	Quiet time.Duration
	// MaxLag is the upper bound on how long a reload may be deferred
	// regardless of Quiet — protects against pathological streams.
	MaxLag time.Duration
	// SwapHint accelerates the ConfigMap atomic-rename detection so
	// Kubernetes deployments do not need to wait the full Quiet window
	// to publish.
	SwapHint time.Duration
}

// WatchOptions bundles the file-watcher knobs. Enabled defaults to
// false; set it explicitly to opt into reload-on-change. Paths and
// Coalesce / CoalesceProfile only apply when Enabled is true.
type WatchOptions struct {
	Enabled         bool
	Paths           []string
	Coalesce        CoalesceOptions
	CoalesceProfile coalesce.Profile
}

// WithWatch installs the file-watcher with the supplied [WatchOptions].
// A zero WatchOptions{} disables the watcher (same as omitting this
// Option). The CoalesceProfile selector applies before the per-field
// Coalesce values, so Coalesce overrides anything the profile set.
func WithWatch(w WatchOptions) Option {
	return func(o *options) {
		o.Watch = w.Enabled
		if len(w.Paths) > 0 {
			o.WatchPaths = append(o.WatchPaths, w.Paths...)
		}
		if w.CoalesceProfile != 0 {
			o.Coalesce = w.CoalesceProfile.Apply()
		}
		if w.Coalesce.Quiet > 0 {
			o.Coalesce.Quiet = w.Coalesce.Quiet
		}
		if w.Coalesce.MaxLag > 0 {
			o.Coalesce.MaxLag = w.Coalesce.MaxLag
		}
		if w.Coalesce.SwapHint > 0 {
			o.Coalesce.SwapHint = w.Coalesce.SwapHint
		}
	}
}

// WithCoalesce overrides just the coalescer windows without touching
// the Watch enabled flag or paths. Useful when a Preset already enabled
// Watch with a profile-based timing set and the caller wants to fine-
// tune one knob:
//
//	fastconf.PresetK8s(K8sOpts{Watch: true, CoalesceProfile: ProfileK8s}),
//	fastconf.WithCoalesce(CoalesceOptions{Quiet: 75*time.Millisecond}),
func WithCoalesce(c CoalesceOptions) Option {
	return func(o *options) {
		if c.Quiet > 0 {
			o.Coalesce.Quiet = c.Quiet
		}
		if c.MaxLag > 0 {
			o.Coalesce.MaxLag = c.MaxLag
		}
		if c.SwapHint > 0 {
			o.Coalesce.SwapHint = c.SwapHint
		}
	}
}
// WithMultiAxisOverlays adds multi-axis overlay layers (region, tier,
// hostname, ...). Each [OverlayAxis] resolves at assemble time to a
// concrete extra overlay directory via its EnvVar /
// DefaultFromHostname rules. Append-only across calls.
func WithMultiAxisOverlays(axes ...OverlayAxis) Option {
	return func(o *options) {
		for _, a := range axes {
			o.OverlayAxes = append(o.OverlayAxes, discovery.AxisSpec{
				Dir:                 a.Dir,
				EnvVar:              a.EnvVar,
				Priority:            a.Priority,
				DefaultFromHostname: a.DefaultFromHostname,
			})
		}
	}
}

func WithSecretRedactor(r SecretRedactor) Option {
	return func(o *options) { o.SecretRedactor = r }
}
func WithProvenance(level ProvenanceLevel) Option {
	return func(o *options) { o.Provenance = level }
}
func WithHistory(n int) Option {
	return func(o *options) {
		if n < 0 {
			n = 0
		}
		o.HistoryCap = n
	}
}

func WithProvider(p contracts.Provider) Option {
	return func(o *options) {
		if p != nil {
			o.Providers = append(o.Providers, p)
		}
	}
}

func WithSource(src contracts.Source, p contracts.Parser) Option {
	return func(o *options) {
		if src != nil {
			o.Providers = append(o.Providers, Bind(src, p))
		}
	}
}

func WithProviderOrdered(ps ...contracts.Provider) Option {
	return func(o *options) {
		base := contracts.PriorityCLI + 100
		for i, p := range ps {
			if p == nil {
				continue
			}
			if p.Priority() != 0 {
				o.DeferredErrs = append(o.DeferredErrs,
					fmt.Errorf("WithProviderOrdered: provider #%d already has Priority=%d", i, p.Priority()))
				continue
			}
			o.Providers = append(o.Providers, iopts.WrapWithPriority(p, base+i))
		}
	}
}

func WithDotEnvAuto(prefix string) Option {
	return func(o *options) { o.DotEnvAutoPrefixes = append(o.DotEnvAutoPrefixes, prefix) }
}

func WithGenerator(g contracts.Generator) Option {
	if g == nil {
		return func(*options) {}
	}
	return func(o *options) { o.Generators = append(o.Generators, g) }
}

func WithTypedHook(h decoder.TypedHook) Option {
	if h == nil {
		return func(*options) {}
	}
	return func(o *options) { o.TypedHooks = append(o.TypedHooks, h) }
}

func WithoutDefaultTypedHooks() Option {
	return func(o *options) { o.TypedHooksOff = true }
}

func WithMergeKeys(keys map[string]string) Option {
	return func(o *options) { iopts.WithMergeKeys(o, keys) }
}

// WithTransformers appends raw-map transformers that run in declared
// order after merge and before the typed decoder. Implementations
// satisfy the root [Transformer] interface (Name + Transform).
func WithTransformers(t ...Transformer) Option {
	return func(o *options) {
		for _, x := range t {
			o.Transformers = append(o.Transformers, x)
		}
	}
}

func WithMigrations(run func(map[string]any) error) Option {
	return func(o *options) {
		if run == nil {
			o.MigrationRun = nil
			return
		}
		o.MigrationRun = MigrationFunc(run)
	}
}

func WithValidator[T any](v func(*T) error) Option {
	if v == nil {
		return func(*options) {}
	}
	wrapped := func(target any) error {
		t, ok := target.(*T)
		if !ok {
			return fcerr.ErrValidator
		}
		return v(t)
	}
	return func(o *options) {
		o.Validators = append(o.Validators, iopts.ValidatorEntry{Fn: wrapped})
	}
}

// ProfileOptions bundles the profile-selection knobs. Single and Multi
// are mutually exclusive: when Multi is non-empty it takes precedence
// and Single is ignored. Expr is the global expression AND-ed with each
// overlay's `_meta.yaml.match` predicate. EnvVar / Default control the
// fallback chain when neither Single nor Multi is set:
//
//	1. ProfileOptions.Single (when non-empty)
//	2. ProfileOptions.Multi  (when non-empty; turns on expression matching)
//	3. $EnvVar / $DefaultProfileEnv
//	4. ProfileOptions.Default
//	5. _meta.yaml's spec.defaultProfile
type ProfileOptions struct {
	// Single is the active profile name for the legacy single-profile
	// path. Set this when you want one overlay subdirectory selected
	// by name. Mutually exclusive with Multi.
	Single string
	// Multi enables expression-based overlay matching by populating
	// the active profile set. Each overlay's `_meta.yaml.match`
	// predicate (or, lacking _meta.yaml, the subdirectory name) is
	// evaluated against this set.
	Multi []string
	// Expr is an additional global expression that must hold for any
	// overlay to be selected. AND-ed with each overlay's per-meta
	// match. Empty disables the global filter.
	Expr string
	// EnvVar names the environment variable read when Single / Multi
	// are both empty. Empty falls back to DefaultProfileEnv
	// ("APP_PROFILE").
	EnvVar string
	// Default is the profile name used when EnvVar is unset / empty.
	Default string
}

// WithProfile installs the supplied [ProfileOptions]. A zero value is
// valid (loads base only).
func WithProfile(p ProfileOptions) Option {
	return func(o *options) {
		if p.Single != "" {
			o.Profile = p.Single
		}
		if len(p.Multi) > 0 {
			o.Profiles = iopts.TrimProfiles(o.Profiles, p.Multi)
		}
		if p.Expr != "" {
			o.ProfileExpr = p.Expr
		}
		if p.EnvVar != "" {
			o.ProfileEnv = p.EnvVar
		}
		if p.Default != "" {
			o.DefaultProf = p.Default
		}
	}
}

func WithPolicy[T any](p policy.Policy[T]) Option {
	return func(o *options) { o.Policies = append(o.Policies, policy.Adapt(p)) }
}

func WithSecretResolver(r SecretResolver) Option {
	return func(o *options) { o.SecretResolver = secret.Resolver(r) }
}
