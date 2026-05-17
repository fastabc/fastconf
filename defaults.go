package fastconf

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/typeinfo"
)

// Default configuration values. These constants define the out-of-the-box
// behaviour of FastConf. All WithXxx options override these values on a
// per-Manager basis. See the individual option docs for semantics.
const (
	// DefaultDir is the configuration root directory used when WithDir is
	// not supplied. It follows the conf.d convention from /etc/conf.d.
	DefaultDir = "conf.d"

	// DefaultProfileEnv is the environment variable FastConf reads when
	// neither WithProfile nor WithProfileEnv is provided.
	DefaultProfileEnv = "APP_PROFILE"

	defaultK8sDir     = "/etc/config"
	defaultK8sProfile = "default"
	defaultSidecarDir = "/etc/fastconfd"
)

// Default coalescer windows for the file-system watcher. Events on a
// single watched parent directory are collapsed into a single reload
// using these timings. See the internal/coalesce package and the
// WithCoalesceQuiet / WithCoalesceMaxLag / WithCoalesceSwapHint options
// for the runtime overrides.
const (
	DefaultCoalesceQuiet    = coalesce.DefaultQuiet
	DefaultCoalesceMaxLag   = coalesce.DefaultMaxLag
	DefaultCoalesceSwapHint = coalesce.DefaultSwapHint
)

// DefaultSidecarHistoryCap is the history ring capacity used by
// PresetSidecar when SidecarOpts.HistoryN is not set.
const DefaultSidecarHistoryCap = 16

// defaultsCache caches the parsed default-tag plan for each *T to keep
// the hot reload path allocation-free after the first invocation.
var defaultsCache = typeinfo.NewCache[[]defaultEntry]()

type defaultEntry struct {
	index  []int
	tagVal string
}

// applyStructDefaults populates zero-valued fields of *t whose
// fastconf tag declares a "default=..." token. The walk recurses into
// nested structs (named or anonymous, value or pointer-to-struct).
// Fields without the tag are left untouched, as are fields the user
// already populated via configuration files or providers.
func applyStructDefaults[T any](t *T) error {
	if t == nil {
		return nil
	}
	v := reflect.ValueOf(t).Elem()
	if !v.IsValid() {
		return nil
	}
	plan := planForType(v.Type())
	for _, e := range plan {
		fv := v.FieldByIndex(e.index)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}
		if !fv.IsZero() {
			continue
		}
		if err := assignFromTag(fv, e.tagVal); err != nil {
			return fmt.Errorf("fastconf default %q: %w", e.tagVal, err)
		}
	}
	return nil
}

func planForType(t reflect.Type) []defaultEntry {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return defaultsCache.GetOrCompute(t, func() []defaultEntry {
		var entries []defaultEntry
		typeinfo.Walk(t, typeinfo.WalkFunc(func(_ string, idx []int, f reflect.StructField, _ *reflect.Type) bool {
			if val, ok := defaultTagValue(f.Tag.Get("fastconf")); ok {
				entries = append(entries, defaultEntry{
					index:  append([]int(nil), idx...),
					tagVal: val,
				})
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			return ft.Kind() == reflect.Struct
		}))
		return entries
	})
}

func defaultTagValue(tag string) (string, bool) {
	if tag == "" {
		return "", false
	}
	for _, p := range strings.Split(tag, ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "default=") {
			return strings.TrimPrefix(p, "default="), true
		}
	}
	return "", false
}

func assignFromTag(fv reflect.Value, raw string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("unsupported default kind %s", fv.Kind())
	}
	return nil
}

// WithStructDefaults installs a transformer that populates zero-valued
// fields of *T from `fastconf:"default=..."` struct tags. It runs once
// per reload, immediately before validation, so user-supplied YAML /
// patch / provider values always win over the tag default.
func WithStructDefaults[T any]() Option {
	return func(o *options) {
		o.structDefaults = func(state any) error {
			ptr, ok := state.(*T)
			if !ok {
				return fmt.Errorf("WithStructDefaults: type mismatch %T", state)
			}
			return applyStructDefaults(ptr)
		}
	}
}

// Defaulter is an optional interface for strongly-typed config structs.
// When *T implements Defaulter, FastConf calls Defaults() once per reload
// AFTER decoding the merged map into *T and AFTER applying struct-tag
// defaults (WithStructDefaults), but BEFORE running validators. This allows
// computed defaults, path normalization, and any logic that cannot be
// expressed in struct tags.
//
// Example:
//
//	type AppConfig struct { Port int; DataDir string }
//
//	func (c *AppConfig) Defaults() {
//	    if c.Port == 0 { c.Port = 8080 }
//	    if c.DataDir == "" { c.DataDir = "/var/lib/myapp" }
//	}
type Defaulter interface {
	Defaults()
}

// WithDefaulterFunc installs a post-decode defaults function for cases where
// *T cannot implement the [Defaulter] interface (e.g., third-party types or
// when modifying the struct definition is not possible).
// It runs at the same point as the interface check: after struct-tag defaults
// and before validators.
func WithDefaulterFunc[T any](fn func(*T)) Option {
	if fn == nil {
		return func(*options) {}
	}
	return func(o *options) {
		o.defaulterFunc = func(state any) {
			if ptr, ok := state.(*T); ok {
				fn(ptr)
			}
		}
	}
}
