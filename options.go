package fastconf

import (
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/scan"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/policy"
)

// Option configures a Manager. options apply in order; later options
// override earlier ones for single-valued settings.
type Option func(*options)

// Axis is one extra overlay dimension (region, tier, host, ...). The
// directory WithDir/<Dir>/<value> is layered above base and profile
// overlays, where value resolves as:
//
//  1. Env present + non-empty       → use that value
//  2. Env present + empty           → skip axis (operator opt-out)
//  3. Env absent + FromHostname     → fall back to os.Hostname()
//  4. otherwise                     → skip axis
//
// Priority orders axes among themselves (higher wins); declaration order
// breaks ties. Every axis sits above the profile overlays and below
// generators and providers.
type Axis struct {
	Dir          string
	Env          string
	Priority     int
	FromHostname bool
}

// WithDecoder selects how the merged tree is decoded into *T: JSON (the
// default, honoring `json:` struct tags) or YAML (honoring `yaml:` tags).
// Source file formats are independent of this choice; JSON numbers also
// remain numeric when decoded through the YAML bridge.
// FastConf reads `fc:` metadata separately for defaults, field-meta and
// secret redaction either way. Symptoms that the default does not match
// your struct: snake_case keys silently dropped (the field only declares a
// `yaml:` tag — New logs a one-time warning), or time.Time fields failing
// to parse. Any other Format fails construction.
func WithDecoder(f Format) Option {
	return func(o *options) {
		switch f {
		case JSON:
			o.YAMLBridge = false
		case YAML:
			o.YAMLBridge = true
		default:
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithDecoder(%s): want JSON or YAML", fcerr.ErrFastConf, f))
		}
	}
}

// UnknownFields selects what happens when the merged configuration has a
// key that no field of *T decodes (typically a misspelling).
type UnknownFields uint8

const (
	// UnknownWarn (the default) logs the first unknown key once and keeps
	// decoding.
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

// WithDir sets the configuration root directory.
func WithDir(dir string) Option { return func(o *options) { o.Dir = dir } }

// WithFS uses f instead of the OS filesystem; file watching is disabled.
func WithFS(f fs.FS) Option { return func(o *options) { o.FS = f } }

// WithStrictMerge toggles strict overlay/merge handling. When enabled the
// overlay scanner rejects files with unknown extensions, requires the
// base directory to exist, and deep-merge type conflicts become hard
// errors instead of last-write-wins. Unknown keys are governed separately
// by WithUnknownFields.
func WithStrictMerge(strict bool) Option { return func(o *options) { o.Strict = strict } }

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

// Watch configures the file watcher for WithDir layers and provider
// WatchPaths. Passing WithWatch turns the watcher on; provider Watch
// channels run regardless.
//
// A burst of filesystem events on one watched directory collapses into one
// reload: it fires after Quiet without further events, or MaxLag after the
// burst started, whichever comes first. SwapHint shortens the wait when a
// Kubernetes ConfigMap atomic swap (..data symlink rename) is recognised.
// Profile selects a timing preset (ProfileK8s, the default, or
// ProfileLocalDev); non-zero Quiet / MaxLag / SwapHint override it.
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

// WithAxes adds extra overlay axes; see [Axis]. Repeated calls accumulate.
func WithAxes(axes ...Axis) Option {
	return func(o *options) {
		if len(o.OverlayAxes)+len(axes) > scan.MaxAxes {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithAxes: at most %d axes fit in the file priority band", fcerr.ErrFastConf, scan.MaxAxes))
			return
		}
		for _, a := range axes {
			o.OverlayAxes = append(o.OverlayAxes, scan.AxisSpec{
				Dir:                 a.Dir,
				EnvVar:              a.Env,
				Priority:            a.Priority,
				DefaultFromHostname: a.FromHostname,
			})
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

// WithRedactor sets how redacted views display a secret value (default
// "***REDACTED***"). Which values are secret is
// decided by fc:"secret" tags and WithSecretPaths.
func WithRedactor(r SecretRedactor) Option {
	return func(o *options) { o.SecretRedactor = r }
}

// WithSecretPaths marks dotted paths as secret for every redacted view
// (Map, Dump, Explain, Plan and committed diffs), in
// addition to `fc:"secret"` tags. "*" matches one path segment, "**"
// matches any number, and list elements are addressed by index. It is the
// only way to redact map-typed configurations such as Manager[map[string]any].
// Repeated calls accumulate.
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

// Profile selects overlay directories under WithDir's overlays/.
//
// The active profile set is Names when non-empty; otherwise the
// comma-separated value of the Env environment variable (default
// DefaultProfileEnv, "APP_PROFILE"); otherwise Default; otherwise
// _meta.yaml's spec.defaultProfile. An overlay directory is included when
// its _meta.yaml match expression (or, lacking one, its name) is satisfied
// by the set; Match is an extra expression every overlay must satisfy.
type Profile struct {
	Names   []string
	Env     string
	Default string
	Match   string
}

// WithProfile installs the profile selection. A zero value loads base
// only unless $APP_PROFILE is set.
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
			o.Policies = append(o.Policies, policy.Adapt(p))
		}
	}
}

// WithSecretResolver sets the resolver for secret references before decoding.
func WithSecretResolver(r SecretResolver) Option {
	return func(o *options) { o.SecretResolver = secret.Resolver(r) }
}
