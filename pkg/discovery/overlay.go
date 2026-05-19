package discovery

import (
	"os"
	"path"
)

// AxisSpec describes a single multi-axis overlay layer. Root fastconf
// re-exports it as fastconf.OverlayAxis via Go type alias so the
// public API surface (WithMultiAxisOverlays) stays unchanged.
//
// Resolution order in ResolveAxes (matches the documented
// fastconf.OverlayAxis semantics):
//
//  1. EnvVar present + non-empty  → use that value
//  2. EnvVar present + empty      → skip axis (operator opt-out)
//  3. EnvVar absent + DefaultFromHostname → fall back to hostFn()
//  4. otherwise                   → skip axis
type AxisSpec struct {
	// Dir is the directory name relative to config root, e.g. "hosts".
	Dir string
	// EnvVar is the environment variable name that selects the active
	// subdirectory. Empty string means the axis has no env override.
	EnvVar string
	// Priority is the base priority for layers in this axis
	// (contracts.BandExtraOverlay or higher).
	Priority int
	// DefaultFromHostname enables an os.Hostname() (or caller-provided
	// hostFn) fallback when EnvVar is absent.
	DefaultFromHostname bool
}

// AxisError reports a non-fatal resolution failure for a single axis,
// so the caller can log the cause and continue. Used today only for
// hostname lookup failures.
type AxisError struct {
	Axis string // axis Dir
	Err  error
}

// ResolveAxes walks specs and returns the ExtraOverlays that should be
// appended to ScanOptions.ExtraOverlays. hostFn is invoked when an axis
// needs its DefaultFromHostname fallback; pass os.Hostname for normal
// runtime use, or a closure that returns a pinned value for
// fastconfctl-plan-style dry-runs.
//
// Errors are non-fatal: the offending axis is skipped and reported via
// the second return value so the caller can log/forward them.
func ResolveAxes(specs []AxisSpec, hostFn func() (string, error)) ([]ExtraOverlay, []AxisError) {
	out := make([]ExtraOverlay, 0, len(specs))
	var errs []AxisError
	for _, ax := range specs {
		var val string
		if ax.EnvVar != "" {
			envVal, present := os.LookupEnv(ax.EnvVar)
			switch {
			case present && envVal != "":
				val = envVal
			case present:
				// Explicit empty → opt-out, regardless of
				// DefaultFromHostname.
				continue
			}
		}
		if val == "" && ax.DefaultFromHostname {
			h, err := hostFn()
			if err != nil {
				errs = append(errs, AxisError{Axis: ax.Dir, Err: err})
				continue
			}
			val = h
		}
		if val == "" {
			continue
		}
		out = append(out, ExtraOverlay{
			Dir:      path.Join(ax.Dir, val),
			Profile:  ax.Dir + ":" + val,
			Priority: ax.Priority,
		})
	}
	return out, errs
}
