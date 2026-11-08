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
	"reflect"
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
)

type options struct {
	Dir string
	FS  fs.FS
	// LoadFile bypasses directory metadata, profiles and layer discovery.
	SkipDiscovery bool
	// ProfileNames, when non-empty, is the active profile set; otherwise
	// the set comes from $ProfileEnv (comma-separated), then ProfileDefault.
	ProfileNames   []string
	ProfileMatch   string
	ProfileEnv     string
	ProfileDefault string
	Strict         bool
	Logger         *slog.Logger
	Providers      []providerEntry

	FileWatch       bool
	CoalesceWindows coalesce.Options
	WatchPaths      []string
	Axes            []scan.AxisSpec

	RegisteredTargetTypes []reflect.Type
	Validators            []func(any) error
	Transforms            []func(map[string]any) error
	Provenance            provenance.Level
	HistoryCap            int
	SecretRedactor        secret.Redactor
	SecretPaths           []string
	// YAMLBridge decodes *T through yaml instead of the default json bridge.
	YAMLBridge     bool
	UnknownFields  UnknownFields
	Tracer         obs.Tracer
	Policies       []anyPolicy
	OptionErrs     []error
	SecretResolver secret.Resolver

	Generators []contracts.Generator

	TypedHooks           []codec.TypedHook
	DefaultTypedHooksOff bool

	MergeKeys map[string]string

	Observers       []Observer
	ObserverTimeout time.Duration

	Tenant string
}

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

func defaultOptions() options {
	base := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return options{
		Dir:             DefaultDir,
		Strict:          false,
		Logger:          base,
		CoalesceWindows: coalesce.ProfileK8s.Apply(),
		Tracer:          obs.NoopTracer{},
	}
}

func resolveOptions(opts []Option) (options, error) {
	o := defaultOptions()
	for _, fn := range opts {
		if fn == nil {
			continue
		}
		fn(&o)
	}
	if o.ObserverTimeout == 0 {
		o.ObserverTimeout = DefaultObserverTimeout
	}
	if len(o.OptionErrs) > 0 {
		// Surface every deferred error from Option closures (e.g. a nil
		// logger) before allocating any state. Each error is emitted individually so operators
		// don't have to read the join chain to spot subsequent failures.
		for _, e := range o.OptionErrs {
			o.Logger.LogAttrs(context.Background(), slog.LevelError, "fastconf: deferred option error", slog.Any("err", e))
		}
		return o, errors.Join(o.OptionErrs...)
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

func checkTypedOptions[T any](o *options) error {
	want := reflect.TypeFor[T]()
	for _, got := range o.RegisteredTargetTypes {
		if got != want {
			return fmt.Errorf("%w: WithValidate/WithPolicy registered for *%v, manager decodes *%v", fcerr.ErrFastConf, got, want)
		}
	}
	return nil
}

func buildTypedHookPlan[T any](o *options) *codec.TypedHookPlan {
	// Build the typed hook plan once. Defaults are included unless
	// WithoutDefaultTypedHooks was set.
	hooks := []codec.TypedHook{}
	if !o.DefaultTypedHooksOff {
		hooks = append(hooks, codec.DefaultTypedHooks()...)
	}
	hooks = append(hooks, o.TypedHooks...)
	if !o.DefaultTypedHooksOff {
		hooks = append(hooks, codec.NamedPrimitiveHook{})
	}
	if len(hooks) == 0 {
		return nil
	}
	// Field selection must match the bridge that decodes the rewritten map.
	var planOpts []codec.PlanOption
	if !o.DefaultTypedHooksOff {
		planOpts = append(planOpts, codec.WithListCoercion())
	}
	if o.YAMLBridge {
		planOpts = append(planOpts, codec.WithYAMLFields())
	}
	return codec.BuildTypedHookPlan(reflect.TypeFor[T](), hooks, planOpts...)
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
	if o.ProfileDefault != "" {
		return []string{o.ProfileDefault}
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
