// Package provenance owns the per-field "where did this value come from" index that the merger
// feeds during reload. FastConf exposes origins through State.Explain and re-exports the public
// provenance types.
package provenance

import (
	"strings"

	"github.com/fastabc/fastconf/confmap"
)

// maxTreeDepth caps recordTree recursion to defeat pathological YAML anchor/alias graphs that produce
// self-referential maps.
const maxTreeDepth = 256

// Origin identifies which configuration layer last wrote a particular dotted field path during the
// merge stage. Origin is opt-in via fastconf.WithProvenance(level): the merger emits an Index only
// when level > Off; the default reload pipeline therefore stays allocation-free.
type Origin struct {
	// Path is the dotted JSON path of the field, e.g. "database.dsn".
	Path string
	// Source is the SourceRef that contributed this value.
	Source SourceRef
	// Value retains the original layer contribution in Full mode: scalars directly,
	// slices cloned, and maps nil to avoid retaining aliased subtrees.
	Value any
}

// Level selects Off (default), TopLevel keys, or Full leaf-path tracking (O(N)).
type Level uint8

const (
	// Off disables origin tracking entirely (default).
	Off Level = iota
	// TopLevel records only top-level (depth=1) keys.
	TopLevel
	// Full records every leaf path — recommended for CLI "explain" use,
	// but adds O(N) work per reload.
	Full
)

// Index maps dotted JSON paths to the chain of layers that wrote to them, oldest first. The last
// element wins the merge.
type Index struct {
	entries map[string][]Origin
	level   Level
}

// NewIndex returns a fresh Index for the given Level. Returns nil for the Off level so callers can
// rely on nil-safe Record / Explain.
func NewIndex(level Level) *Index {
	if level == Off {
		return nil
	}
	return &Index{entries: map[string][]Origin{}, level: level}
}

// Record annotates path with src. Patches and providers append, the chain order is preserved so
// callers can reconstruct merge history.
func (o *Index) Record(path string, src SourceRef) {
	o.RecordValue(path, src, nil)
}

// RecordValue is the value-carrying counterpart of Record; used by the merger to capture the raw
// layer value when Full level is enabled.
func (o *Index) RecordValue(path string, src SourceRef, val any) {
	if o == nil {
		return
	}
	if o.level == TopLevel && strings.Contains(path, ".") {
		return
	}
	o.entries[path] = append(o.entries[path], Origin{Path: path, Source: src, Value: val})
}

// RecordTree walks a freshly-merged map and records every leaf path that exists in it as having
// been written by src. Used by the merger after deep-merging a layer so that overlay paths win.
func (o *Index) RecordTree(prefix string, m map[string]any, src SourceRef) {
	o.recordTreeDepth(prefix, m, src, 0)
}

func (o *Index) recordTreeDepth(prefix string, m map[string]any, src SourceRef, depth int) {
	if o == nil || depth > maxTreeDepth {
		return
	}
	for k, v := range m {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch nested := v.(type) {
		case map[string]any:
			if o.level == Full {
				o.recordTreeDepth(full, nested, src, depth+1)
			} else {
				o.Record(full, src)
			}
		case []any:
			// Full history needs a deep clone: merge may alias layer elements, then
			// secret/hooks/transforms mutate them. Shallow copies could expose resolved
			// secrets instead of the original layer references through Explain.
			if o.level == Full {
				o.RecordValue(full, src, confmap.CloneValue(nested))
			} else {
				o.Record(full, src)
			}
		default:
			// Scalar leaf. Value is a Full-level feature only; at TopLevel
			// record the path without a value (see Origin.Value doc).
			if o.level == Full {
				o.RecordValue(full, src, v)
			} else {
				o.Record(full, src)
			}
		}
	}
}

// Explain returns the chain of layers that contributed to the given dotted field path. The chain
// is oldest→newest; the last element "won" the merge. An unknown path yields nil.
func (o *Index) Explain(path string) []Origin {
	if o == nil {
		return nil
	}
	chain := o.entries[path]
	if chain == nil {
		return nil
	}
	out := make([]Origin, len(chain))
	for i, origin := range chain {
		out[i] = cloneOrigin(origin)
	}
	return out
}

func cloneOrigin(origin Origin) Origin {
	origin.Value = confmap.CloneValue(origin.Value)
	return origin
}

// SourceRef describes the metadata for a single config layer that participated in a merge.
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
	// WithOverride. It sits above all provider layers so override
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
