package coalesce

import "time"

// Profile names a preset for the three Coalescer windows. Use
// ProfileK8s in production (matches ConfigMap atomic-swap latencies)
// and ProfileLocalDev when iterating against an editor that writes
// via unlink-then-create cascades.
type Profile int

const (
	// ProfileK8s is the production preset (the default).
	// Quiet 30ms, MaxLag 250ms, SwapHint 5ms — tuned for K8s ConfigMap
	// atomic-swap with a fast trailing-CHMOD drain.
	ProfileK8s Profile = iota

	// ProfileLocalDev relaxes the windows for editor-driven workflows
	// (Vim/Emacs unlink-write-rename cascades that can span 20-50ms).
	// Quiet 80ms, MaxLag 500ms, SwapHint 20ms.
	ProfileLocalDev
)

// Apply returns the Options corresponding to this profile.
func (p Profile) Apply() Options {
	switch p {
	case ProfileLocalDev:
		return Options{
			Quiet:    80 * time.Millisecond,
			MaxLag:   500 * time.Millisecond,
			SwapHint: 20 * time.Millisecond,
		}
	default:
		return Options{
			Quiet:    DefaultQuiet,
			MaxLag:   DefaultMaxLag,
			SwapHint: DefaultSwapHint,
		}
	}
}
