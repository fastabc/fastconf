// Package state holds snapshot, source, and reload-cause primitives used by
// the public fastconf facade and internal manager implementation.
package state

import "github.com/fastabc/fastconf/internal/provenance"

type SourceRef = provenance.SourceRef
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
