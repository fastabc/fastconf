// Package state holds snapshot, source, and reload-cause primitives used by
// the public fastconf facade and internal manager implementation.
package state

import "github.com/fastabc/fastconf/internal/fctypes"

type SourceRef = fctypes.SourceRef
type LayerKind = fctypes.LayerKind

const (
	LayerUnknown   = fctypes.LayerUnknown
	LayerMerge     = fctypes.LayerMerge
	LayerPatch     = fctypes.LayerPatch
	LayerProvider  = fctypes.LayerProvider
	LayerSecret    = fctypes.LayerSecret
	LayerGenerator = fctypes.LayerGenerator
	LayerOverride  = fctypes.LayerOverride
)
