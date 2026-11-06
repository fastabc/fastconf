package fastconf

import (
	"github.com/fastabc/fastconf/internal/provenance"
)

// Public aliases preserve the domain types used by snapshot diagnostics.
type (
	// Origin records one source contribution.
	Origin = provenance.Origin
	// ProvenanceLevel controls the granularity of source tracking.
	ProvenanceLevel = provenance.Level
)

// Provenance levels select how much source history is retained.
const (
	ProvenanceOff      = provenance.Off
	ProvenanceTopLevel = provenance.TopLevel
	ProvenanceFull     = provenance.Full
)

// SourceRef identifies a merged source layer.
type SourceRef = provenance.SourceRef

// LayerKind classifies a source layer.
type LayerKind = provenance.LayerKind

// Layer kinds classify the source of a configuration contribution.
const (
	LayerUnknown   = provenance.LayerUnknown
	LayerMerge     = provenance.LayerMerge
	LayerPatch     = provenance.LayerPatch
	LayerProvider  = provenance.LayerProvider
	LayerSecret    = provenance.LayerSecret
	LayerGenerator = provenance.LayerGenerator
	LayerOverride  = provenance.LayerOverride
)
