package fctypes

// SourceRef describes the metadata for a single config layer that
// participated in a merge.
type SourceRef struct {
	Path     string
	Kind     LayerKind
	Profile  string
	Priority int
	Codec    string
	Revision string
	Stale    bool
}

// LayerKind identifies the merge semantics of a layer.
type LayerKind uint8

const (
	LayerUnknown LayerKind = iota
	LayerMerge
	LayerPatch
	LayerProvider
	LayerSecret
	LayerGenerator
)

func (k LayerKind) String() string {
	switch k {
	case LayerMerge:
		return "merge"
	case LayerPatch:
		return "patch"
	case LayerProvider:
		return "provider"
	case LayerSecret:
		return "secret"
	case LayerGenerator:
		return "generator"
	default:
		return "unknown"
	}
}
