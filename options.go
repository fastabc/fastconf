package fastconf

// Options are consolidated here: every fastconf.WithXxx builder lives in
// this file (paired with its options-struct field) so the public Option
// surface stays discoverable in one place.
// Tracer- and AuditSink-related options (WithTracer / WithAuditSink)
// live with their interface definitions in the obs_* files.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/feature"
	"github.com/fastabc/fastconf/pkg/flog"
	"github.com/fastabc/fastconf/pkg/provider"
	"github.com/fastabc/fastconf/pkg/transform"
	"github.com/fastabc/fastconf/policy"
)

// ---------------------------------------------------------------------
// Option type + options struct + defaults
// ---------------------------------------------------------------------

// Option configures Manager behavior.
type Option func(*options)

type options struct {
	dir         string
	fsys        fs.FS // Overrides dir in tests.
	profile     string
	profiles    []string // Multi-profile active set.
	profileExpr string   // Global profile match expression override.
	profileEnv  string
	defaultProf string
	strict      bool
	logger      *slog.Logger
	log         *flog.Logger // zerolog-style fluent wrapper over logger; refreshed in New() after all options apply.
	providers   []contracts.Provider

	watch         bool
	watchInterval time.Duration
	watchPaths    []string
	overlayAxes   []OverlayAxis // multi-axis overlay configuration

	metrics        metricsBridge
	validators     []validatorEntry
	transformers   []Transformer
	provenance     ProvenanceLevel
	historyCap     int
	secretRedactor SecretRedactor
	structDefaults func(any) error
	defaulterFunc  func(any) // called after structDefaults if *T implements Defaulter or via WithDefaulterFunc
	codecBridge    codecBridge
	migrationRun   MigrationApplier
	auditSinks     []AuditSink
	tracer         Tracer
	policies       []policy.AnyPolicy
	deferredErrs   []error
	rawMapHook     func(map[string]any) // called with merged map before codec decode
	secretResolver SecretResolver       // optional pre-decode secret decryption hook

	featureExtract func(any) map[string]feature.Rule

	generators []contracts.Generator

	typedHooks    []decoder.TypedHook // extra hooks beyond defaults
	typedHooksOff bool                // opt-out of DefaultTypedHooks()

	mergeKeys map[string]string // programmatic strategic-merge keys

	diffReporters        []DiffReporter
	diffReporterQueueCap int // per-reporter bounded queue; 0 → defaultDiffReporterQueueCap

	dotEnvAutoPrefixes []string // deferred WithDotEnvAuto resolution

	// providerRegistry is the optional Manager-local factory registry
	// installed via WithProviderRegistry. nil → only the process-wide
	// default registry is consulted.
	providerRegistry *ProviderRegistry
	// pendingByName accumulates WithProviderByName lookups; resolved
	// in New() once every Option has applied so registry ordering is
	// irrelevant.
	pendingByName []pendingByName
}

// defaultDiffReporterQueueCap is the per-reporter queue depth used when
// WithDiffReporterQueueCap is not set. Chosen to absorb reasonable bursts
// of changes without unbounded goroutine fan-out on slow reporters.
const defaultDiffReporterQueueCap = 64

func defaultOptions() options {
	base := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return options{
		dir: DefaultDir,
		// profileEnv default lives in effectiveProfile so _meta.yaml can
		// override it when WithProfileEnv is not used.
		profileEnv:    "",
		defaultProf:   "",
		strict:        false,
		logger:        base,
		log:           flog.New(base),
		watchInterval: DefaultDebounceInterval,
		metrics:       newMetricsBridge(noopMetrics{}),
		tracer:        noopTracer{},
	}
}

// refreshLog re-derives the fluent flog.Logger from the current
// *slog.Logger. Called once at the end of New() after every Option has
// run, so WithLogger can swap the backend without callers worrying
// about ordering relative to the fluent wrapper.
func (o *options) refreshLog() {
	if o.logger == nil {
		o.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	o.log = flog.New(o.logger)
}

// ---------------------------------------------------------------------
// Codec bridge selector
// ---------------------------------------------------------------------

// codecBridge picks the encoder used by decodeInto on the reload path.
type codecBridge uint8

const (
	bridgeJSON codecBridge = iota // default; pairs with canonicalHash byte reuse
	bridgeYAML                    // legacy v0.6 behaviour for yaml-only struct tags
)

// BridgeJSON and BridgeYAML are the exported aliases for use with WithCodecBridge.
const (
	BridgeJSON = bridgeJSON
	BridgeYAML = bridgeYAML
)

// WithCodecBridge selects how the merged map[string]any is round-tripped
// into *T. The default bridgeJSON pairs with the SHA-256 hash so a
// reload only marshals the document once. Choose BridgeYAML if your
// configuration struct only carries yaml tags and you cannot add json
// tags; this is the v0.6 behaviour and slightly slower.
func WithCodecBridge(b codecBridge) Option {
	return func(o *options) { o.codecBridge = b }
}

// WithRawMapAccess installs a read hook that is called with the fully merged
// map[string]any immediately after all transformers run and just before the
// map is decoded into *T via the configured codec bridge.
//
// Downstream adapters use this hook to work around type-mismatch issues that
// the codec bridge cannot resolve on its own:
//   - Extract a sub-tree (e.g. "protocols") as raw data to populate a
//     json.RawMessage field without going through a yaml.Marshal / Unmarshal
//     round-trip that loses type information.
//   - Read string-form values (e.g. "30s") that json.Unmarshal cannot convert
//     natively into time.Duration fields, and use them alongside a separate
//     validator or defaulter.
//
// The callback is invoked synchronously on the single reload goroutine.
// The map argument is the live merged tree — callers MUST NOT retain a
// reference beyond the call or mutate the map.  Use WithTransformers if
// mutation of the merged tree before decode is required.
//
// Example — capture the raw "protocols" sub-tree so a validator can convert
// it to json.RawMessage independent of the codec bridge:
//
//	var rawProtocols map[string]any
//	fastconf.New[Config](ctx,
//	    fastconf.WithRawMapAccess(func(root map[string]any) {
//	        if p, ok := root["protocols"].(map[string]any); ok {
//	            rawProtocols = p
//	        }
//	    }),
//	    fastconf.WithValidator(func(cfg *Config) error {
//	        if rawProtocols != nil {
//	            b, _ := json.Marshal(rawProtocols)
//	            cfg.Protocols = b
//	        }
//	        return nil
//	    }),
//	)
func WithRawMapAccess(fn func(root map[string]any)) Option {
	if fn == nil {
		return func(*options) {}
	}
	return func(o *options) { o.rawMapHook = fn }
}

// ---------------------------------------------------------------------
// Filesystem + logging
// ---------------------------------------------------------------------

// WithDir sets the configuration root directory. Default: DefaultDir.
func WithDir(dir string) Option { return func(o *options) { o.dir = dir } }

// WithFS sets the fs.FS used to load configs. It overrides WithDir.
func WithFS(f fs.FS) Option { return func(o *options) { o.fsys = f } }

// WithStrict enables strict file and merge validation.
func WithStrict(strict bool) Option { return func(o *options) { o.strict = strict } }

// WithLogger injects the logger used by FastConf. The default discards
// every log line (io.Discard), so callers must opt in to see them. Pass
// nil to keep the current default.
//
// Any slog.Handler-backed *slog.Logger works: stdlib JSON/Text handlers,
// the phuslu adapter under integrations/log/phuslu, the zerolog adapter
// under integrations/log/zerolog, or any third-party Handler. Internally
// FastConf wraps the logger in pkg/flog for zerolog-style fluent calls,
// so swapping the backend never affects call-site code.
//
// If you already have an slog.Handler instead of *slog.Logger, wrap it:
//
//	fastconf.WithLogger(slog.New(myHandler))
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// ---------------------------------------------------------------------
// Watcher + debounce
// ---------------------------------------------------------------------

// WithWatch enables file-system driven reloads.
func WithWatch(enabled bool) Option { return func(o *options) { o.watch = enabled } }

// WithDebounceInterval sets the watch debounce window. Default: DefaultDebounceInterval.
func WithDebounceInterval(d time.Duration) Option {
	return func(o *options) { o.watchInterval = d }
}

// WithWatchPaths appends additional paths to watch.
func WithWatchPaths(paths ...string) Option {
	return func(o *options) { o.watchPaths = append(o.watchPaths, paths...) }
}

// ---------------------------------------------------------------------
// Multi-axis overlays (base + regions/<r> + zones/<z> + hosts/<h>)
// ---------------------------------------------------------------------

// OverlayAxis describes a single overlay axis: a directory under the config
// root that contains named subdirectories, where the active subdirectory is
// determined by an environment variable.
//
// Example:
//
//	OverlayAxis{Dir: "hosts", EnvVar: "HOST", Priority: 3200, DefaultFromHostname: true}
//
// With HOST=ua and config root "config/", FastConf loads all files under
// "config/hosts/ua/" as additional file layers with priority 3200.
// Files in this axis override base layers (priority 1000-1999) and standard
// overlays (2000-2999), but are themselves overridden by providers (8000+).
//
// Axis value resolution order:
//  1. If EnvVar is non-empty and the environment variable is set to a non-empty
//     value, that value is used.
//  2. If EnvVar is non-empty and the environment variable is explicitly empty,
//     the axis is skipped (operator opt-out).
//  3. If the environment variable is absent (not set at all) and DefaultFromHostname
//     is true, os.Hostname() is used as the axis value.
//  4. If EnvVar is empty ("") and DefaultFromHostname is true, os.Hostname() is
//     used unconditionally (no env var override is possible — hostname-only axis).
//  5. Otherwise the axis is skipped.
type OverlayAxis struct {
	Dir      string // directory name relative to config root, e.g. "hosts"
	EnvVar   string // environment variable that selects the active subdirectory
	Priority int    // base priority for layers in this axis (suggested: 3000+)
	// DefaultFromHostname controls whether os.Hostname() is used as the axis
	// value when EnvVar is absent from the environment. This is useful for
	// host-specific overlays that should activate automatically based on the
	// machine name without requiring an explicit environment variable.
	//
	// When EnvVar is non-empty: DefaultFromHostname only activates if the
	// variable is absent (not set at all). Setting the variable to an empty
	// string explicitly disables the axis, giving operators a way to opt out.
	//
	// When EnvVar is empty (""): DefaultFromHostname always activates —
	// the axis unconditionally uses os.Hostname() with no env var override.
	DefaultFromHostname bool
}

// WithMultiAxisOverlays registers one or more overlay axes. Each axis maps
// an environment variable to a subdirectory under the config root. When the
// environment variable is set to a name that matches an existing subdirectory,
// that subdirectory's files are loaded as additional file layers at the
// declared priority level, after base and overlays but before providers.
//
// Axes are loaded in declaration order; assign increasing Priority values to
// establish a clear override hierarchy (e.g. regions < zones < hosts).
//
// Directories that do not exist are silently skipped.
//
// When DefaultFromHostname is true on an axis, os.Hostname() is used as the
// axis value if the environment variable is absent (not set). The env var
// still takes precedence when present, and an explicitly empty env var
// disables the axis.
//
// Usage:
//
//	fastconf.New[Config](ctx,
//	    fastconf.WithDir("config"),
//	    fastconf.WithMultiAxisOverlays(
//	        fastconf.OverlayAxis{Dir: "regions", EnvVar: "REGION", Priority: 3000},
//	        fastconf.OverlayAxis{Dir: "zones",   EnvVar: "ZONE",   Priority: 3100},
//	        fastconf.OverlayAxis{Dir: "hosts",   EnvVar: "HOST",   Priority: 3200, DefaultFromHostname: true},
//	    ),
//	)
func WithMultiAxisOverlays(axes ...OverlayAxis) Option {
	return func(o *options) {
		o.overlayAxes = append(o.overlayAxes, axes...)
	}
}

// ---------------------------------------------------------------------
// Metrics / secret redactor / provenance / history
// ---------------------------------------------------------------------

// WithMetrics injects the metrics sink used by the reload pipeline.
func WithMetrics(m MetricsSink) Option {
	return func(o *options) {
		if m != nil {
			o.metrics = newMetricsBridge(m)
		}
	}
}

// WithSecretRedactor installs the secret redactor used by dumps and snapshots.
func WithSecretRedactor(r SecretRedactor) Option {
	return func(o *options) { o.secretRedactor = r }
}

// WithProvenance enables field-level origin tracking at the requested
// level. The default (ProvenanceOff) keeps the reload pipeline
// allocation-free; ProvenanceTopLevel adds O(top-level keys) work and
// ProvenanceFull adds O(leaves). Once enabled,
// Manager.Snapshot().Origins().Explain("a.b.c") returns the chain of
// layers that wrote to that path, oldest→newest.
func WithProvenance(level ProvenanceLevel) Option {
	return func(o *options) { o.provenance = level }
}

// WithHistory keeps the last n successfully committed states in an
// in-memory ring buffer so Manager.Rollback / Manager.History can
// surface them. The default (0) disables history. Each retained state
// holds one *T plus its sources slice, so size the buffer with care
// for very large configs.
func WithHistory(n int) Option {
	return func(o *options) {
		if n < 0 {
			n = 0
		}
		o.historyCap = n
	}
}

// ---------------------------------------------------------------------
// Providers + bytes sources
// ---------------------------------------------------------------------

// WithProvider registers an external provider merged after file layers.
func WithProvider(p contracts.Provider) Option {
	return func(o *options) {
		if p != nil {
			o.providers = append(o.providers, p)
		}
	}
}

// WithProviderOrdered is a let-me-keep-it-simple helper for users who prefer
// the Viper "last call wins" mental model over FastConf's explicit Priority()
// integers. It wraps each supplied provider in a thin priorityOverride that
// assigns a strictly increasing priority starting just above PriorityCLI, so
// providers later in the argument list always win.
//
// Use it when you have a fixed call order and don't want to think about the
// priority table. For mixed deployments (file + env + multiple remote
// providers) the explicit Priority() approach is still clearer.
func WithProviderOrdered(ps ...contracts.Provider) Option {
	return func(o *options) {
		base := contracts.PriorityCLI + 100
		for i, p := range ps {
			if p == nil {
				continue
			}
			if p.Priority() != 0 {
				o.deferredErrs = append(o.deferredErrs,
					fmt.Errorf("WithProviderOrdered: provider #%d already has Priority=%d", i, p.Priority()))
				continue
			}
			o.providers = append(o.providers, wrapWithPriority(p, base+i))
		}
	}
}

// priorityOverride wraps a non-Resumable Provider with an explicit priority.
type priorityOverride struct {
	contracts.Provider
	priority int
}

func (p *priorityOverride) Priority() int { return p.priority }

// priorityOverrideResumable preserves Resumable.WatchFrom when wrapping
// a provider whose dynamic type also implements contracts.Resumable.
type priorityOverrideResumable struct {
	contracts.Provider
	contracts.Resumable
	priority int
}

func (p *priorityOverrideResumable) Priority() int { return p.priority }

// wrapWithPriority returns the smallest wrapper type that preserves
// every interface p satisfies (Provider always, Resumable when present).
func wrapWithPriority(p contracts.Provider, prio int) contracts.Provider {
	if r, ok := p.(contracts.Resumable); ok {
		return &priorityOverrideResumable{Provider: p, Resumable: r, priority: prio}
	}
	return &priorityOverride{Provider: p, priority: prio}
}

// WithDotEnvAuto auto-discovers ".env" files in the config directory
// (WithDir value) and the current working directory, loading them as the
// lowest-priority provider.
//
// Resolution is deferred to the end of option application so option order
// no longer matters — WithDotEnvAuto("APP_") placed before WithDir("conf.d")
// works correctly. The prefix is stashed and resolved once just before
// New() builds its Manager. This is the one Option whose mechanics cannot
// be replaced by a single WithProvider call (because it needs the final
// o.dir value); other env / .env / label / CLI / bytes sugars were removed
// in v0.14 — use WithProvider(provider.NewEnv(...)), WithProvider(
// provider.NewDotEnv(...)), WithProvider(provider.NewLabels(...)),
// WithProvider(provider.NewCLI(...)), WithProvider(provider.NewBytes(...))
// directly.
func WithDotEnvAuto(prefix string) Option {
	return func(o *options) {
		o.dotEnvAutoPrefixes = append(o.dotEnvAutoPrefixes, prefix)
	}
}

// applyDeferredDotEnvAuto resolves every pending WithDotEnvAuto call
// against the final o.dir value. Invoked at the top of New() after all
// user Options have run.
func (o *options) applyDeferredDotEnvAuto() {
	for _, prefix := range o.dotEnvAutoPrefixes {
		paths := provider.AutoDotEnvPaths(o.dir)
		if len(paths) > 0 {
			o.providers = append(o.providers, provider.NewDotEnv(prefix, paths...))
		}
	}
	o.dotEnvAutoPrefixes = nil
}

// WithGenerator registers a Source generator that runs during the
// assemble stage of every reload. Generators synthesise layers
// dynamically (Kustomize ConfigMapGenerator / SecretGenerator style):
// inject build info, query a downward-api volume, or shell out for a
// JSON blob. A failing generator aborts the reload and preserves the
// previous *State[T]. See contracts.Generator.
func WithGenerator(g contracts.Generator) Option {
	if g == nil {
		return func(*options) {}
	}
	return func(o *options) { o.generators = append(o.generators, g) }
}

// WithTypedHook registers an additional decoder hook beyond the default
// Duration / IP / URL / Regex set. Hooks rewrite merged map leaves into the
// typed wire form that encoding/json can natively unmarshal into *T's
// strongly-typed fields ("30s" → int64 nanoseconds, "10.0.0.1" → canonical
// IP string, etc).
//
// Hooks are evaluated in (defaults ++ extras) order; the first Match wins per
// field. Use WithoutDefaultTypedHooks to drop the built-in set when a project
// wants its own end-to-end policy.
func WithTypedHook(h decoder.TypedHook) Option {
	if h == nil {
		return func(*options) {}
	}
	return func(o *options) { o.typedHooks = append(o.typedHooks, h) }
}

// WithoutDefaultTypedHooks disables the built-in Duration / IP / URL / Regex
// hooks. Use it when the application has installed its own typed-hook policy
// via WithTypedHook and the defaults would conflict.
func WithoutDefaultTypedHooks() Option {
	return func(o *options) { o.typedHooksOff = true }
}

// WithMergeKeys installs Kustomize-style strategic merge keys without requiring
// a _meta.yaml file. Each entry maps a dotted path in the merged tree to the
// field name that identifies "the same item" across overlays. Programmatic
// option values are merged with any _meta.yaml mergeKeys; programmatic entries
// win on conflict.
func WithMergeKeys(keys map[string]string) Option {
	return func(o *options) {
		if o.mergeKeys == nil {
			o.mergeKeys = map[string]string{}
		}
		maps.Copy(o.mergeKeys, keys)
	}
}

// ---------------------------------------------------------------------
// Transformer / MigrationApplier interfaces + builders
// ---------------------------------------------------------------------

// Transformer mutates the merged configuration tree before it is decoded
// into the user's strongly typed snapshot.
//
// This is a type alias for [transform.Transformer]: any value satisfying
// pkg/transform.Transformer satisfies fastconf.Transformer too — they
// are the same Go type. The alias keeps fastconf's option surface
// ergonomic while letting the built-in transformer set (Defaults /
// SetIfAbsent / EnvSubst / DeletePaths / Aliases) live in its own
// package.
type Transformer = transform.Transformer

// MigrationApplier rewrites the merged configuration tree before
// transformers and decode run. The single-method shape lets a plain
// function adapt via [MigrationFunc].
//
// The reload pipeline invokes Migrate exactly once per reload on the
// single writer goroutine; implementations therefore do not need to
// be safe for concurrent calls. Returning an error aborts the reload
// and preserves the previous *State[T].
type MigrationApplier interface {
	Migrate(map[string]any) error
}

// MigrationFunc adapts a plain function to [MigrationApplier].
type MigrationFunc func(map[string]any) error

// Migrate implements [MigrationApplier].
func (fn MigrationFunc) Migrate(root map[string]any) error { return fn(root) }

// WithTransformers appends post-merge / pre-decode transformers to the
// reload pipeline. They run in order, after every layer has been
// merged/patched but before the merged tree is decoded into the user's
// strongly-typed *T. A failing transformer aborts the reload and the
// previous state is preserved.
//
// Transformers are designed to host cross-cutting concerns such as
// applying defaults, env-var interpolation, key aliases / deprecations,
// and stripping operator-only fields.
func WithTransformers(t ...Transformer) Option {
	return func(o *options) { o.transformers = append(o.transformers, t...) }
}

// WithMigrations installs a schema-migration callback that runs after
// the merged map is assembled but before transformers and decode. It
// addresses the long-lived-config / evolving-struct mismatch by letting
// operators rename or restructure ageing keys on the fly.
func WithMigrations(run func(map[string]any) error) Option {
	return func(o *options) {
		if run == nil {
			o.migrationRun = nil
			return
		}
		o.migrationRun = MigrationFunc(run)
	}
}

// ---------------------------------------------------------------------
// Validator
// ---------------------------------------------------------------------

// validatorEntry stores a type-erased validator. The exported generic
// helper WithValidator[T] is responsible for type-asserting back to *T.
type validatorEntry struct {
	fn func(any) error
}

// WithValidator registers a strongly-typed validator. Runs after the
// merged document has been decoded into *T but BEFORE the new state is
// published. If any registered validator returns an error, the reload
// fails atomically: the previous state is preserved and Get() continues
// to return the prior value.
//
// Validators are the canonical way to enforce cross-field invariants
// (e.g. "if mTLS is enabled, certificateFile must be non-empty") that
// cannot be expressed in struct tags or JSON Schema.
//
// Multiple validators may be registered; they run in registration order
// and the first error short-circuits the rest.
//
//	fastconf.New[AppConfig](ctx,
//	    fastconf.WithValidator(func(cfg *AppConfig) error {
//	        if cfg.Server.Addr == "" { return errors.New("server.addr required") }
//	        return nil
//	    }),
//	)
//
// Validators must be deterministic and side-effect-free; they MAY run
// repeatedly during shadow loads.
func WithValidator[T any](v func(*T) error) Option {
	if v == nil {
		return func(*options) {}
	}
	wrapped := func(target any) error {
		t, ok := target.(*T)
		if !ok {
			// Should be unreachable: WithValidator[T] is only meaningful
			// when used with the matching New[T]. Guard regardless.
			return ErrValidator
		}
		return v(t)
	}
	return func(o *options) {
		o.validators = append(o.validators, validatorEntry{fn: wrapped})
	}
}

// ---------------------------------------------------------------------
// Profile selectors
// ---------------------------------------------------------------------

// WithProfile sets the active overlay profile explicitly.
func WithProfile(p string) Option { return func(o *options) { o.profile = p } }

// WithProfiles activates the multi-profile model. When at least one
// profile is supplied, FastConf evaluates each overlay subdirectory's
// optional `_meta.yaml.match:` boolean expression against this active
// set instead of the legacy single-profile lookup. Subdirectories
// without a match expression fall back to membership: they are included
// iff their directory name is one of the supplied profiles. WithProfile
// remains supported for the simple single-tag case and is preserved as
// a fallback when WithProfiles is not used.
func WithProfiles(p ...string) Option {
	return func(o *options) {
		for _, x := range p {
			x = strings.TrimSpace(x)
			if x != "" {
				o.profiles = append(o.profiles, x)
			}
		}
	}
}

// WithProfileExpr appends a global match expression to every overlay.
func WithProfileExpr(expr string) Option { return func(o *options) { o.profileExpr = expr } }

// WithProfileEnv sets the environment variable used to resolve the profile.
func WithProfileEnv(name string) Option { return func(o *options) { o.profileEnv = name } }

// WithDefaultProfile sets the fallback profile when no explicit profile exists.
func WithDefaultProfile(p string) Option { return func(o *options) { o.defaultProf = p } }

// effectiveProfile resolves the profile using options, _meta.yaml, and defaults.
func (o *options) effectiveProfile(metaProfileEnv, metaDefault string) string {
	if o.profile != "" {
		return o.profile
	}
	env := o.profileEnv
	if env == "" {
		env = metaProfileEnv
	}
	if env == "" {
		env = DefaultProfileEnv
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	if o.defaultProf != "" {
		return o.defaultProf
	}
	return metaDefault
}

// ---------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------

// WithPolicy registers a typed Policy[T] that is evaluated on the
// reload goroutine after decode + validation but BEFORE the atomic
// state swap. Multiple WithPolicy calls fan-out (all policies run,
// findings aggregate). A SeverityError finding aborts the reload
// and the previous *State[T] remains in place — the failure-safe
// invariant is preserved.
//
// Use:
//
//	mgr, err := fastconf.New[MyApp](ctx,
//	    fastconf.WithDir("conf.d"),
//	    fastconf.WithPolicy(policy.Func[MyApp]{
//	        N: "deny-debug-in-prod",
//	        Fn: func(_ context.Context, in policy.Input[MyApp]) ([]policy.Violation, error) {
//	            if in.Config.Profile == "prod" && in.Config.Debug {
//	                return []policy.Violation{{Path: "debug", Severity: policy.SeverityError}}, nil
//	            }
//	            return nil, nil
//	        },
//	    }),
//	)
func WithPolicy[T any](p policy.Policy[T]) Option {
	return func(o *options) {
		o.policies = append(o.policies, policy.Adapt(p))
	}
}

// ErrPolicyDenied is returned by reload() when one or more SeverityError
// violations fired. The error message lists every violation; callers
// can inspect the structured slice via errors.As(err, *PolicyError).
var ErrPolicyDenied = errors.New("fastconf: policy denied")

// PolicyError aggregates the violations that aborted a reload. It
// satisfies errors.Is(ErrPolicyDenied) so callers don't need to know
// the concrete type to special-case policy failures.
type PolicyError struct {
	Violations []policy.Violation
}

func (e *PolicyError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s@%s: %s", v.Rule, v.Path, v.Message))
	}
	return "fastconf: policy denied: " + strings.Join(parts, "; ")
}

func (e *PolicyError) Is(target error) bool {
	return target == ErrPolicyDenied || target == ErrFastConf
}

// evaluatePolicies runs every registered policy against cfg and
// returns a *PolicyError if any SeverityError fires; warnings are
// returned via the second slice for the caller to forward to logs
// and audit.
func (m *Manager[T]) evaluatePolicies(ctx context.Context, cfg *T, reason string) (*PolicyError, []policy.Violation) {
	if len(m.opts.policies) == 0 {
		return nil, nil
	}
	var errs []policy.Violation
	var warns []policy.Violation
	for _, p := range m.opts.policies {
		vs, err := p.EvaluateAny(ctx, cfg, reason, m.tenantTag())
		if err != nil {
			errs = append(errs, policy.Violation{
				Rule:     p.Name(),
				Message:  "evaluation error: " + err.Error(),
				Severity: policy.SeverityError,
			})
			continue
		}
		for _, v := range vs {
			if v.Rule == "" {
				v.Rule = p.Name()
			}
			if v.Severity == policy.SeverityError {
				errs = append(errs, v)
			} else {
				warns = append(warns, v)
			}
		}
	}
	if len(errs) == 0 {
		return nil, warns
	}
	return &PolicyError{Violations: errs}, warns
}

// tenantTag returns the Tenant id stamped on this manager (if any).
// Resolved once during New() and cached, so callers on the reload
// hot path (policy evaluation) pay zero allocations per call.
func (m *Manager[T]) tenantTag() string { return m.tenant }
