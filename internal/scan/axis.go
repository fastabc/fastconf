package scan

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

var errUnsafeAxisValue = errors.New("scan: axis value must be a single path segment")

// AxisSpec is the scanner form of fastconf.Axis; see docs/cookbook/sources/layers.md
// for environment presence, empty opt-out, hostname fallback and priority rules.
// Its field names are reachable through fastconf.Option and remain stable in v1.
type AxisSpec struct {
	// Dir is the directory name relative to config root, e.g. "hosts".
	Dir string
	// EnvVar is the environment variable name that selects the active
	// subdirectory. Empty string means the axis has no env override.
	EnvVar string
	// Priority orders axes relative to one another; higher values win.
	Priority int
	// DefaultFromHostname enables an os.Hostname() (or caller-provided
	// hostFn) fallback when EnvVar is absent.
	DefaultFromHostname bool
}

// AxisError reports a non-fatal resolution failure for a single axis, so the caller can log the
// cause and continue.
type AxisError struct {
	Axis string // axis Dir
	Err  error
}

// ResolveAxes selects and orders extra overlays; hostFn supplies absent-env hostname
// fallbacks. Failed axes are skipped and reported separately as non-fatal errors.
func ResolveAxes(specs []AxisSpec, hostFn func() (string, error)) ([]AxisOverlay, []AxisError) {
	out := make([]AxisOverlay, 0, len(specs))
	var errs []AxisError
	specs = slices.Clone(specs)
	slices.SortStableFunc(specs, func(a, b AxisSpec) int { return cmp.Compare(a.Priority, b.Priority) })
	for rank, ax := range specs {
		var val string
		if ax.EnvVar != "" {
			envVal, present := os.LookupEnv(ax.EnvVar)
			switch {
			case present && envVal != "":
				val = envVal
			case present:
				// Explicit empty → opt-out, regardless of
				// FromHostname.
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
		if val == "." || val == ".." || strings.ContainsAny(val, `/\`) || !fs.ValidPath(val) {
			errs = append(errs, AxisError{Axis: ax.Dir, Err: fmt.Errorf("%w: %q", errUnsafeAxisValue, val)})
			continue
		}
		out = append(out, AxisOverlay{
			Dir:      path.Join(ax.Dir, val),
			Profile:  ax.Dir + ":" + val,
			Priority: BandAxis + rank*overlayStride,
		})
	}
	return out, errs
}
