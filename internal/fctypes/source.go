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
	// LayerOverride is used for one-shot in-process overrides passed via
	// WithSourceOverride. It sits above all provider layers so override
	// values always win at merge time.
	LayerOverride
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
	case LayerOverride:
		return "override"
	default:
		return "unknown"
	}
}
