package fastconf

import (
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"time"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/scan"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/policy"
)

// Option configures a Manager. options apply in order; later options
// override earlier ones for single-valued settings. nil values are ignored.
type Option func(*options)

// Axis adds WithDir/<Dir>/<value> above profile overlays and below generators.
// A present Env variable supplies its value; an empty value disables the axis.
// When absent, FromHostname uses os.Hostname. Higher Priority wins; declaration
// order breaks ties. See docs/cookbook/sources/layers.md.
type Axis struct {
	Dir          string
	Env          string
	Priority     int
	FromHostname bool
}

// WithDecodeFormat selects the struct bridge and typed-value hashing format:
// JSON (default, json tags) or YAML (yaml tags). Source formats are independent;
// JSON numbers remain numeric in the YAML bridge. Custom marshalers must include
// all relevant fields deterministically. Diagnostic views still use JSON names and
// omit json:"-"; fc metadata applies with either bridge. Other formats fail construction.
// See docs/design/spec.md#decoding.
func WithDecodeFormat(f Format) Option {
	return func(o *options) {
		switch f {
		case JSON:
			o.YAMLBridge = false
		case YAML:
			o.YAMLBridge = true
		default:
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithDecodeFormat(%s): want JSON or YAML", fcerr.ErrFastConf, f))
		}
	}
}

// UnknownFields selects what happens when the merged configuration has a
// key that no field of *T decodes (typically a misspelling).
type UnknownFields uint8

const (
	// UnknownWarn (the default) logs the decoder's unknown-field warning and
	// keeps decoding. Repeated identical warnings are suppressed. Configure
	// WithLogger to receive warnings; the default logger discards output.
	UnknownWarn UnknownFields = iota
	// UnknownError fails the reload with ErrDecode naming the key.
	UnknownError
	// UnknownIgnore silently drops unknown keys.
	UnknownIgnore
)

// WithUnknownFields sets the unknown-key policy; see [UnknownFields]. It
// only applies to struct targets; map-typed configurations accept any key.
func WithUnknownFields(mode UnknownFields) Option {
	return func(o *options) { o.UnknownFields = mode }
}

// DefaultDir is the configuration root directory used when WithDir is
// not supplied. It follows the conf.d convention from /etc/conf.d.
const DefaultDir = "conf.d"

// WithDir sets the configuration root directory.
func WithDir(dir string) Option { return func(o *options) { o.Dir = dir } }

// WithFS uses f instead of the OS filesystem; file watching is disabled.
func WithFS(f fs.FS) Option { return func(o *options) { o.FS = f } }

// WithStrictMerge rejects unknown file extensions, special files, a missing base
// directory and raw merge type conflicts. WithUnknownFields governs struct keys.
// A true spec.strict in root metadata also enables strictness; false cannot undo it.
func WithStrictMerge(strict bool) Option { return func(o *options) { o.Strict = strict } }

// WithLogger replaces the default silent logger, including UnknownWarn output.
// A nil logger fails construction.
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

// Watch configures directory coalescing: Quiet after the last event or MaxLag
// since the first, shortened by SwapHint for Kubernetes swaps. ProfileK8s is the
// default preset; non-zero timing fields override it. WithWatch enables file
// watching for directory layers and provider WatchPaths; native Watch runs independently.
type Watch struct {
	Paths    []string
	Quiet    time.Duration
	MaxLag   time.Duration
	SwapHint time.Duration
	Profile  CoalesceProfile
}

// WithWatch enables the file watcher; see [Watch].
func WithWatch(w Watch) Option {
	return func(o *options) {
		o.Watch = true
		o.WatchPaths = append(o.WatchPaths, w.Paths...)
		o.Coalesce = w.Profile.Apply()
		if w.Quiet > 0 {
			o.Coalesce.Quiet = w.Quiet
		}
		if w.MaxLag > 0 {
			o.Coalesce.MaxLag = w.MaxLag
		}
		if w.SwapHint > 0 {
			o.Coalesce.SwapHint = w.SwapHint
		}
	}
}

// Default coalescer windows for the file-system watcher. Events on a
// single watched parent directory are collapsed into a single reload
// using these timings. See the internal/coalesce package; runtime
// overrides go through WithWatch(Watch{...}).
const (
	DefaultCoalesceQuiet    = coalesce.DefaultQuiet
	DefaultCoalesceMaxLag   = coalesce.DefaultMaxLag
	DefaultCoalesceSwapHint = coalesce.DefaultSwapHint
)

// CoalesceProfile is the public re-export of the coalescer preset
// selector. Use ProfileK8s in production and ProfileLocalDev when
// iterating against editors that write via unlink-rename cascades.
type CoalesceProfile = coalesce.Profile

// CoalesceProfile values mirroring the internal/coalesce constants.
const (
	ProfileK8s      = coalesce.ProfileK8s
	ProfileLocalDev = coalesce.ProfileLocalDev
)

// WithAxes adds extra overlay axes; see [Axis]. Repeated calls accumulate.
func WithAxes(axes ...Axis) Option {
	return func(o *options) {
		if len(o.OverlayAxes)+len(axes) > scan.MaxAxes {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithAxes: at most %d axes fit in the file priority band", fcerr.ErrFastConf, scan.MaxAxes))
			return
		}
		for _, a := range axes {
			o.OverlayAxes = append(o.OverlayAxes, scan.AxisSpec{Dir: a.Dir, EnvVar: a.Env, Priority: a.Priority, DefaultFromHostname: a.FromHostname})
		}
	}
}

// WithTenant tags the manager with a tenant id. The id is stamped on every
// ReloadCause and passed to policies as policy.Input.Tenant. Keep your own
// map of per-tenant managers; each is an ordinary New call with its own
// WithTenant.
func WithTenant(id string) Option {
	return func(o *options) { o.Tenant = id }
}

// SecretRedactor converts secret values into display values.
type SecretRedactor = secret.Redactor

// SecretRef identifies an opaque secret reference.
type SecretRef = secret.Ref

// SecretResolver recognizes and resolves secret references.
type SecretResolver = secret.Resolver

// SecretResolverFunc adapts functions to SecretResolver.
type SecretResolverFunc = secret.ResolverFunc

// WithRedactor sets how redacted views display a secret value (default
// "***REDACTED***"). Which values are secret is
// decided by fc:"secret" tags and WithSecretPaths.
func WithRedactor(r SecretRedactor) Option {
	return func(o *options) { o.SecretRedactor = r }
}

// WithSecretPaths accumulates dotted secret patterns for every diagnostic view,
// including map targets. * matches one segment, ** any number; lists use indices.
// Literal dots cannot be escaped: mask their parent or use wildcards. Explain
// conservatively masks ambiguous dotted paths, potentially hiding nested keys.
func WithSecretPaths(patterns ...string) Option {
	return func(o *options) { o.SecretPaths = append(o.SecretPaths, patterns...) }
}

// WithProvenance sets the amount of source attribution retained in snapshots.
func WithProvenance(level ProvenanceLevel) Option {
	return func(o *options) { o.Provenance = level }
}

// WithHistory retains up to n committed snapshots; zero disables history.
// A negative capacity makes New return an error.
func WithHistory(n int) Option {
	return func(o *options) {
		if n < 0 {
			o.DeferredErrs = append(o.DeferredErrs, fmt.Errorf("%w: WithHistory: capacity must be non-negative", fcerr.ErrFastConf))
			return
		}
		o.HistoryCap = n
	}
}

// WithProvider registers providers. Merge order follows each provider's
// Describe().Priority (higher wins); equal priorities merge in declaration
// order, across calls as well as within one. nil entries are ignored.
func WithProvider(ps ...contracts.Provider) Option {
	return func(o *options) {
		for _, p := range ps {
			if p != nil {
				o.addProvider(p)
			}
		}
	}
}

// WithGenerator registers generators; nil entries are ignored.
func WithGenerator(gs ...contracts.Generator) Option {
	return func(o *options) {
		for _, g := range gs {
			if g != nil {
				o.Generators = append(o.Generators, g)
			}
		}
	}
}

// WithTypedHook adds typed decode hooks (string → time.Duration, net.IP,
// ...) that run on top of the defaults; nil entries are ignored.
func WithTypedHook(hs ...codec.TypedHook) Option {
	return func(o *options) {
		for _, h := range hs {
			if h != nil {
				o.TypedHooks = append(o.TypedHooks, h)
			}
		}
	}
}

// WithoutDefaultTypedHooks disables the built-in typed decode hooks.
func WithoutDefaultTypedHooks() Option {
	return func(o *options) { o.TypedHooksOff = true }
}

// WithMergeKeys sets the identity field for keyed-slice merging at each dotted path.
func WithMergeKeys(keys map[string]string) Option {
	return func(o *options) { o.addMergeKeys(keys) }
}

// WithTransform appends functions that rewrite the merged tree after merge
// and before decoding into *T (rename keys, inject computed values,
// migrate schema versions). They run in declaration order, across calls,
// on the reload goroutine; an error aborts the reload. nil entries fail
// construction.
func WithTransform(fns ...func(map[string]any) error) Option {
	return func(o *options) {
		for i, fn := range fns {
			if fn == nil {
				o.DeferredErrs = append(o.DeferredErrs,
					fmt.Errorf("%w: WithTransform: nil function at #%d", fcerr.ErrFastConf, i))
				continue
			}
			o.Transforms = append(o.Transforms, fn)
		}
	}
}

// WithValidate appends validators that run after decoding and defaults. The
// first failing validator aborts the reload (Plan runs them all and reports
// each). nil entries are ignored.
func WithValidate[T any](fns ...func(*T) error) Option {
	return func(o *options) {
		for _, v := range fns {
			if v == nil {
				continue
			}
			o.TypedFor = append(o.TypedFor, reflect.TypeFor[T]())
			o.Validators = append(o.Validators, func(target any) error {
				t, ok := target.(*T)
				if !ok {
					return fcerr.ErrInvalid
				}
				return v(t)
			})
		}
	}
}

// DefaultProfileEnv is the environment variable FastConf reads when
// neither Profile.Names nor Profile.Env is set.
const DefaultProfileEnv = "APP_PROFILE"

// Profile selects overlays using Names, then comma-separated Env (default APP_PROFILE),
// then Default, then root spec.defaultProfile. Each directory's match expression
// (or its name when absent) must match the active set; Match adds a global filter.
// See docs/cookbook/sources/layers.md for metadata and selection examples.
type Profile struct {
	Names   []string
	Env     string
	Default string
	Match   string
}

// WithProfile installs the profile selection. A zero value uses the
// environment and root _meta.yaml defaults described by Profile; base
// alone is loaded when none selects a profile.
func WithProfile(p Profile) Option {
	return func(o *options) {
		o.ProfileNames = trimProfiles(nil, p.Names)
		o.ProfileEnv = p.Env
		o.DefaultProf = p.Default
		o.ProfileMatch = p.Match
	}
}

// WithPolicy appends policies evaluated before committing a configuration.
func WithPolicy[T any](ps ...policy.Policy[T]) Option {
	return func(o *options) {
		for _, p := range ps {
			if p != nil {
				o.TypedFor = append(o.TypedFor, reflect.TypeFor[T]())
				o.Policies = append(o.Policies, adaptPolicy(p))
			}
		}
	}
}

// WithSecretResolver sets the resolver for secret references before decoding.
func WithSecretResolver(r SecretResolver) Option {
	return func(o *options) { o.SecretResolver = secret.Resolver(r) }
}
