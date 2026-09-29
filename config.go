package fastconf

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

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/obs"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/scan"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/policy"
)

// providerEntry is a registered provider plus the metadata read once from
// its Describe method. Registration-time overrides (ordered priorities)
// edit Info instead of wrapping the provider.
type providerEntry struct {
	Provider contracts.Provider
	Info     contracts.ProviderInfo
}

// addProvider registers p with its self-described metadata.
func (o *options) addProvider(p contracts.Provider) {
	o.Providers = append(o.Providers, providerEntry{Provider: p, Info: contracts.Describe(p)})
}

type options struct {
	Dir string
	FS  fs.FS
	// ProfileNames, when non-empty, is the active profile set; otherwise
	// the set comes from $ProfileEnv (comma-separated), then DefaultProf.
	ProfileNames []string
	ProfileMatch string
	ProfileEnv   string
	DefaultProf  string
	Strict       bool
	Logger       *slog.Logger
	Providers    []providerEntry

	Watch       bool
	Coalesce    coalesce.Options
	WatchPaths  []string
	OverlayAxes []scan.AxisSpec

	Validators     []func(any) error
	Transforms     []func(map[string]any) error
	Provenance     provenance.Level
	HistoryCap     int
	SecretRedactor secret.Redactor
	SecretPaths    []string
	// YAMLBridge decodes *T through yaml instead of the default json bridge.
	YAMLBridge     bool
	UnknownFields  UnknownFields
	Tracer         obs.Tracer
	Policies       []policy.AnyPolicy
	DeferredErrs   []error
	SecretResolver secret.Resolver

	Generators []contracts.Generator

	TypedHooks    []codec.TypedHook
	TypedHooksOff bool

	MergeKeys map[string]string

	Observers       []Observer
	ObserverTimeout time.Duration

	Tenant string
}

func defaultOptions() options {
	base := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return options{
		Dir:      DefaultDir,
		Strict:   false,
		Logger:   base,
		Coalesce: coalesce.ProfileK8s.Apply(),
		Tracer:   obs.NoopTracer{},
	}
}

// activeProfiles resolves the profile set: explicit names, then the
// comma-separated value of the profile env var (option, _meta.yaml, then
// DefaultProfileEnv), then the option default, then _meta.yaml's default.
func (o *options) activeProfiles(metaProfileEnv, metaDefault string) []string {
	if len(o.ProfileNames) > 0 {
		return o.ProfileNames
	}
	env := o.ProfileEnv
	if env == "" {
		env = metaProfileEnv
	}
	if env == "" {
		env = DefaultProfileEnv
	}
	if v := trimProfiles(nil, strings.Split(os.Getenv(env), ",")); len(v) > 0 {
		return v
	}
	if o.DefaultProf != "" {
		return []string{o.DefaultProf}
	}
	if metaDefault != "" {
		return []string{metaDefault}
	}
	return nil
}

func (o *options) addMergeKeys(keys map[string]string) {
	if o.MergeKeys == nil {
		o.MergeKeys = map[string]string{}
	}
	maps.Copy(o.MergeKeys, keys)
}

func trimProfiles(dst []string, vals []string) []string {
	for _, x := range vals {
		x = strings.TrimSpace(x)
		if x != "" {
			dst = append(dst, x)
		}
	}
	return dst
}

func resolveOptions(opts []Option) (options, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	if o.ObserverTimeout == 0 {
		o.ObserverTimeout = DefaultObserverTimeout
	}
	if len(o.DeferredErrs) > 0 {
		// Surface every deferred error from Option closures (e.g. a nil
		// logger) before allocating any state. Each error is emitted individually so operators
		// don't have to read the join chain to spot subsequent failures.
		for _, e := range o.DeferredErrs {
			o.Logger.LogAttrs(context.Background(), slog.LevelError, "fastconf: deferred option error", slog.Any("err", e))
		}
		return o, errors.Join(o.DeferredErrs...)
	}
	// Validate user-supplied profile expression at startup so syntax
	// errors fail loudly instead of silently matching nothing per overlay.
	if o.ProfileMatch != "" {
		if _, err := scan.Compile(o.ProfileMatch); err != nil {
			return o, fmt.Errorf("%w: WithProfile.Match: %v", fcerr.ErrFastConf, err)
		}
	}
	return o, nil
}

// Defaults and coalescing presets are overridden by per-manager options.

// Default configuration values.
const (
	// DefaultDir is the configuration root directory used when WithDir is
	// not supplied. It follows the conf.d convention from /etc/conf.d.
	DefaultDir = "conf.d"

	// DefaultObserverTimeout bounds each Observer.Observe call.
	DefaultObserverTimeout = 2 * time.Second

	// DefaultProfileEnv is the environment variable FastConf reads when
	// neither Profile.Names nor Profile.Env is set.
	DefaultProfileEnv = "APP_PROFILE"
)

// Default coalescer windows for the file-system watcher. Events on a
// single watched parent directory are collapsed into a single reload
// using these timings. See the internal/coalesce package; runtime
// overrides go through WithWatch(Watch{...}).
const (
	DefaultCoalesceQuiet    = coalesce.DefaultQuiet
	DefaultCoalesceMaxLag   = coalesce.DefaultMaxLag
	DefaultCoalesceSwapHint = coalesce.DefaultSwapHint
)

// Defaulter supplies computed defaults after decoding and struct-tag defaults,
// before validation. It is called once per pipeline execution when *T implements it.
type Defaulter interface {
	Defaults()
}

// CoalesceProfile is the public re-export of the coalescer preset
// selector. Use ProfileK8s in production and ProfileLocalDev when
// iterating against editors that write via unlink-rename cascades.
type CoalesceProfile = coalesce.Profile

// CoalesceProfile values mirroring the internal/coalesce constants.
const (
	ProfileK8s      = coalesce.ProfileK8s
	ProfileLocalDev = coalesce.ProfileLocalDev
)
