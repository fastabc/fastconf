package fastconf

import (
	"time"

	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/scan"
)

// ReloadCause is the audit-friendly explanation of a successful commit.
// It is carried by the Committed observer event and surfaced on State[T].Cause so
// downstream tooling can trace an in-process change back to the event
// (file change, provider push, Reload) that drove it.
type ReloadCause struct {
	// Reason mirrors the reloadRequest reason ("initial",
	// "provider:vault://...", "manual", "watcher", ...). Stable string
	// safe for log labels and metric dimensions.
	Reason string
	// At is the snapshot commit time, or the preview time for Plan.
	At time.Time
	// Revisions captures every provider's reported revision at the time
	// of assemble (provider name -> revision string). Empty for plain
	// file-only configurations.
	Revisions map[string]string
	// Tenant is the WithTenant id; empty unless the manager was tagged.
	Tenant string
	// Key, when non-empty, identifies the watched parent directory whose
	// fsnotify event burst triggered this reload. Populated only for
	// file-system driven reloads (the coalescer keys bursts by parent
	// dir); empty for manual, provider-driven, and initial reloads.
	Key string
}

const providerPathPrefix = "provider://"

// mapLayerKind translates an internal scan.Kind into the public
// LayerKind enum reported via SourceRef.
func mapLayerKind(k scan.Kind) LayerKind {
	switch k {
	case scan.KindMerge:
		return LayerMerge
	case scan.KindPatch:
		return LayerPatch
	default:
		return LayerUnknown
	}
}

// collectProviderRevisions extracts the per-provider revision map from sources;
// only LayerProvider entries with non-empty Revision are included.
func collectProviderRevisions(sources []SourceRef) map[string]string {
	var out map[string]string
	for _, s := range sources {
		if s.Kind != LayerProvider || s.Revision == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		// Strip the "provider://" prefix for readability.
		name := s.Path
		if len(name) > len(providerPathPrefix) && name[:len(providerPathPrefix)] == providerPathPrefix {
			name = name[len(providerPathPrefix):]
		}
		out[name] = s.Revision
	}
	return out
}

// Public aliases preserve the domain types used by snapshot diagnostics.
type (
	// Origin records one source contribution.
	Origin = provenance.Origin
	// ProvenanceLevel controls the granularity of source tracking.
	ProvenanceLevel = provenance.Level
)

const (
	ProvenanceOff      = provenance.Off
	ProvenanceTopLevel = provenance.TopLevel
	ProvenanceFull     = provenance.Full
)

// SourceRef identifies a merged source layer.
type SourceRef = provenance.SourceRef

// LayerKind classifies a source layer.
type LayerKind = provenance.LayerKind

const (
	LayerUnknown   = provenance.LayerUnknown
	LayerMerge     = provenance.LayerMerge
	LayerPatch     = provenance.LayerPatch
	LayerProvider  = provenance.LayerProvider
	LayerSecret    = provenance.LayerSecret
	LayerGenerator = provenance.LayerGenerator
	LayerOverride  = provenance.LayerOverride
)
