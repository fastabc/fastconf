// Package provenance owns the per-field "where did this value come from"
// index that the merger feeds during reload. The public surface
// (fastconf.Origin, fastconf.OriginIndex, fastconf.ProvenanceLevel)
// re-exports these types via Go type aliases, so callers of
// State.Origins / State.Explain see no change.
package provenance

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fastabc/fastconf/internal/fctypes"
)

// maxDepth caps recordTree recursion to defeat pathological YAML
// anchor/alias graphs that produce self-referential maps.
const maxDepth = 256

// Origin identifies which configuration layer last wrote a particular
// dotted field path during the merge stage. Origin is opt-in via
// fastconf.WithProvenance(level): the merger emits an Index only when
// level > Off; the default reload pipeline therefore stays
// allocation-free.
type Origin struct {
	// Path is the dotted JSON path of the field, e.g. "database.dsn".
	Path string
	// Source is the SourceRef that contributed this value.
	Source fctypes.SourceRef
	// Value is the per-layer value as it appeared in this Source's
	// contribution before downstream layers overrode it. Only populated
	// when Full level is enabled and the value is a JSON leaf (non-map).
	// Map values are intentionally left nil to avoid retaining large
	// subtrees.
	Value any
}

// Level controls how aggressively the merger records field origins.
//
//	Off       — default; no recording, zero overhead.
//	TopLevel  — only track top-level keys (cheap).
//	Full      — track every leaf path (linear in tree size).
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

// Index maps dotted JSON paths to the chain of layers that wrote to
// them, oldest first. The last element wins the merge.
type Index struct {
	entries map[string][]Origin
	level   Level
}

// NewIndex returns a fresh Index for the given Level. Returns nil for
// the Off level so callers can rely on nil-safe Record / Explain.
func NewIndex(level Level) *Index {
	if level == Off {
		return nil
	}
	return &Index{entries: map[string][]Origin{}, level: level}
}

// Record annotates path with src. Patches and providers append, the
// chain order is preserved so callers can reconstruct merge history.
func (o *Index) Record(path string, src fctypes.SourceRef) {
	o.RecordValue(path, src, nil)
}

// RecordValue is the value-carrying counterpart of Record; used by the
// merger to capture the raw layer value when Full level is enabled.
func (o *Index) RecordValue(path string, src fctypes.SourceRef, val any) {
	if o == nil {
		return
	}
	if o.level == TopLevel && strings.Contains(path, ".") {
		return
	}
	o.entries[path] = append(o.entries[path], Origin{Path: path, Source: src, Value: val})
}

// RecordTree walks a freshly-merged map and records every leaf path
// that exists in it as having been written by src. Used by the merger
// after deep-merging a layer so that overlay paths win.
func (o *Index) RecordTree(prefix string, m map[string]any, src fctypes.SourceRef) {
	o.recordTreeDepth(prefix, m, src, 0)
}

func (o *Index) recordTreeDepth(prefix string, m map[string]any, src fctypes.SourceRef, depth int) {
	if o == nil || depth > maxDepth {
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
		default:
			o.RecordValue(full, src, v)
		}
	}
}

// Explain returns the chain of layers that contributed to the given
// dotted field path. The chain is oldest→newest; the last element
// "won" the merge. An unknown path yields nil.
func (o *Index) Explain(path string) []Origin {
	if o == nil {
		return nil
	}
	chain := o.entries[path]
	if chain == nil {
		return nil
	}
	out := make([]Origin, len(chain))
	copy(out, chain)
	return out
}

// Paths returns every recorded path in deterministic order, useful for
// CLI listings and tests.
func (o *Index) Paths() []string {
	if o == nil {
		return nil
	}
	out := make([]string, 0, len(o.entries))
	for k := range o.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Format renders an explain entry as one line per origin.
func (o *Index) Format(path string) string {
	chain := o.Explain(path)
	if len(chain) == 0 {
		return fmt.Sprintf("%s: <no origin recorded>", path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", path)
	for i, e := range chain {
		marker := " "
		if i == len(chain)-1 {
			marker = "*" // winner
		}
		fmt.Fprintf(&b, "  %s [%d] %s (%s)\n", marker, e.Source.Priority, e.Source.Path, e.Source.Kind)
	}
	return b.String()
}
