package fastconf

import (
	"io/fs"
	"time"
)

// Preset constructors compose the most common
// combinations of WithXxx options so users do not have to learn the
// full 30+ Option surface up-front. Each Preset returns a single
// Option that, when applied, fans out to a curated set of underlying
// options. Users can mix Presets with explicit With* calls; later
// options override earlier ones (last-write-wins per field), so the
// pattern is:
//
//	mgr, err := fastconf.New[T](ctx,
//	    fastconf.PresetK8s(fastconf.K8sOpts{Dir: "/etc/myapp", ProfileEnv: fastconf.DefaultProfileEnv}),
//	    fastconf.WithStrict(false), // override the Preset default
//	)

// K8sOpts captures the common knobs for a Kubernetes deployment that
// reads ConfigMaps mounted at a known directory and selects a profile
// from an environment variable populated by the Pod spec.
type K8sOpts struct {
	Dir        string // ConfigMap mount path (default "/etc/config")
	ProfileEnv string // env var to read profile from (default DefaultProfileEnv)
	Default    string // default profile if env empty (default "default")
	Watch      bool   // enable fsnotify (recommended)
	Debounce   time.Duration
}

// PresetK8s returns the canonical option bundle for K8s side-by-side
// ConfigMap deployments: directory load, profile from env, watch on,
// strict mode (fail loud on unknown fields).
func PresetK8s(p K8sOpts) Option {
	return func(o *options) {
		dir := p.Dir
		if dir == "" {
			dir = defaultK8sDir
		}
		env := p.ProfileEnv
		if env == "" {
			env = DefaultProfileEnv
		}
		def := p.Default
		if def == "" {
			def = defaultK8sProfile
		}
		WithDir(dir)(o)
		WithProfileEnv(env)(o)
		WithDefaultProfile(def)(o)
		WithWatch(p.Watch)(o)
		WithStrict(true)(o)
		if p.Debounce > 0 {
			WithDebounceInterval(p.Debounce)(o)
		}
	}
}

// SidecarOpts captures the common knobs for cmd/fastconfd-style
// deployments where the manager is hosted by an in-cluster process
// that exposes the config over HTTP/SSE.
type SidecarOpts struct {
	Dir      string
	HistoryN int  // history ring capacity (default DefaultSidecarHistoryCap)
	Watch    bool // typically true for sidecars
	Strict   bool
}

// PresetSidecar returns options tuned for a sidecar daemon: bigger
// history ring (so /events SSE consumers can replay), watch on by
// default, less strict so unknown fields warn instead of fail.
func PresetSidecar(p SidecarOpts) Option {
	return func(o *options) {
		dir := p.Dir
		if dir == "" {
			dir = defaultSidecarDir
		}
		n := p.HistoryN
		if n <= 0 {
			n = DefaultSidecarHistoryCap
		}
		WithDir(dir)(o)
		WithWatch(p.Watch)(o)
		WithHistory(n)(o)
		WithStrict(p.Strict)(o)
	}
}

// TestingOpts captures the common knobs for hermetic unit/integration
// tests: pass an fs.FS (often testing/fstest.MapFS), pin a profile,
// disable watch, and force strict so tests catch typos eagerly.
type TestingOpts struct {
	FS      fs.FS
	Profile string
}

// PresetTesting returns options tuned for hermetic tests. Watch is
// always disabled; strict is always on.
func PresetTesting(p TestingOpts) Option {
	return func(o *options) {
		if p.FS != nil {
			WithFS(p.FS)(o)
		}
		if p.Profile != "" {
			WithProfile(p.Profile)(o)
		}
		WithWatch(false)(o)
		WithStrict(true)(o)
	}
}

// HierarchicalOpts captures the common knobs for deployments that use the
// base + regions/<r> + zones/<z> + hosts/<h> directory layout driven by
// environment variables.
type HierarchicalOpts struct {
	Dir      string        // config root directory (default DefaultDir)
	RegionEnv string       // env var for region axis (default "REGION")
	ZoneEnv  string        // env var for zone axis (default "ZONE")
	HostEnv  string        // env var for host axis (default "HOST")
	Watch    bool          // enable fsnotify hot-reload
	Debounce time.Duration // debounce interval (0 = DefaultDebounceInterval)
}

// PresetHierarchical returns options for the standard multi-axis deployment
// pattern: base layer always loaded, then regions (if $REGION is set), then
// zones (if $ZONE is set), then hosts (if $HOST is set or hostname matches a
// subdirectory). Providers still override all file layers.
//
// The hosts axis uses DefaultFromHostname: true, so it automatically activates
// based on os.Hostname() when the host env var is not set. Set the env var
// explicitly to an empty string to disable host-specific overlays.
//
// Example directory layout:
//
//	config/
//	├── base/           <- always loaded (priority 1000-1999)
//	├── regions/
//	│   └── eu-west/    <- loaded when $REGION=eu-west (priority 3000-3099)
//	├── zones/
//	│   └── az1/        <- loaded when $ZONE=az1       (priority 3100-3199)
//	└── hosts/
//	    └── web-01/     <- loaded when $HOST=web-01 or hostname=web-01 (priority 3200-3299)
func PresetHierarchical(p HierarchicalOpts) Option {
	return func(o *options) {
		dir := p.Dir
		if dir == "" {
			dir = DefaultDir
		}
		regionEnv := p.RegionEnv
		if regionEnv == "" {
			regionEnv = "REGION"
		}
		zoneEnv := p.ZoneEnv
		if zoneEnv == "" {
			zoneEnv = "ZONE"
		}
		hostEnv := p.HostEnv
		if hostEnv == "" {
			hostEnv = "HOST"
		}
		WithDir(dir)(o)
		WithMultiAxisOverlays(
			OverlayAxis{Dir: "regions", EnvVar: regionEnv, Priority: 3000},
			OverlayAxis{Dir: "zones", EnvVar: zoneEnv, Priority: 3100},
			OverlayAxis{Dir: "hosts", EnvVar: hostEnv, Priority: 3200, DefaultFromHostname: true},
		)(o)
		WithWatch(p.Watch)(o)
		if p.Debounce > 0 {
			WithDebounceInterval(p.Debounce)(o)
		}
	}
}
