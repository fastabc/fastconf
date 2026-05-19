package fastconf

// User-visible default constants and the small Option set that wires
// internal/pipeline's struct-tag defaults + Defaulter interface into the
// reload pipeline. The reflective plan / cache lives in
// internal/pipeline/defaults.go so this file can stay thin.

import (
	"fmt"

	iopts "github.com/fastabc/fastconf/internal/options"
	"github.com/fastabc/fastconf/internal/pipeline"
)

// Default configuration values. These constants define the out-of-the-box
// behaviour of FastConf. All WithXxx options override these values on a
// per-Manager basis. See the individual option docs for semantics.
const (
	// DefaultDir is the configuration root directory used when WithDir is
	// not supplied. It follows the conf.d convention from /etc/conf.d.
	DefaultDir = iopts.DefaultDir

	// DefaultProfileEnv is the environment variable FastConf reads when
	// neither WithProfile(ProfileOptions{Single}) nor
	// WithProfile(ProfileOptions{EnvVar}) sets one explicitly.
	DefaultProfileEnv = iopts.DefaultProfileEnv

	defaultK8sDir     = "/etc/config"
	defaultK8sProfile = "default"
	defaultSidecarDir = "/etc/fastconfd"
)

// Default coalescer windows for the file-system watcher. Events on a
// single watched parent directory are collapsed into a single reload
// using these timings. See the internal/coalesce package; runtime
// overrides go through WithCoalesce(CoalesceOptions{...}).
const (
	DefaultCoalesceQuiet    = iopts.DefaultCoalesceQuiet
	DefaultCoalesceMaxLag   = iopts.DefaultCoalesceMaxLag
	DefaultCoalesceSwapHint = iopts.DefaultCoalesceSwapHint
)

// DefaultSidecarHistoryCap is the history ring capacity used by
// PresetSidecar when SidecarOpts.HistoryN is not set.
const DefaultSidecarHistoryCap = iopts.DefaultSidecarHistoryCap

// WithStructDefaults installs a transformer that populates zero-valued
// fields of *T from `fastconf:"default=..."` struct tags. It runs once
// per reload, immediately before validation, so user-supplied YAML /
// patch / provider values always win over the tag default.
func WithStructDefaults[T any]() Option {
	return func(o *options) {
		o.StructDefaults = func(state any) error {
			ptr, ok := state.(*T)
			if !ok {
				return fmt.Errorf("WithStructDefaults: type mismatch %T", state)
			}
			return pipeline.ApplyStructDefaults(ptr)
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

// WithDefaults installs a post-decode defaults function for cases where
// *T cannot implement the [Defaulter] interface (e.g., third-party types or
// when modifying the struct definition is not possible). It is the
// function-form counterpart to [Defaulter] and runs at the same point in
// the pipeline.
//
// Defaults precedence (each step only fills zero / unset fields left by
// the previous step):
//
//  1. [WithStructDefaults] — `fastconf:"default=..."` struct tags
//  2. [Defaulter] interface — *T.Defaults() if implemented
//  3. WithDefaults — explicit fn, last to run
//
// All three run BEFORE validators, so [WithValidator] sees the populated
// value.
func WithDefaults[T any](fn func(*T)) Option {
	if fn == nil {
		return func(*options) {}
	}
	return func(o *options) {
		o.DefaulterFunc = func(state any) {
			if ptr, ok := state.(*T); ok {
				fn(ptr)
			}
		}
	}
}
