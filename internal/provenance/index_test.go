package provenance

import (
	"testing"
)

func TestProvenance_DepthGuard(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	idx := NewIndex(Full)
	idx.RecordTree("", m, SourceRef{Path: "loop"})
	if idx == nil {
		t.Fatal("nil idx")
	}
}

// TopLevel records paths but not Value (Value is a Full-level feature).
func TestProvenance_TopLevelOmitsValue(t *testing.T) {
	idx := NewIndex(TopLevel)
	idx.RecordTree("", map[string]any{"name": "x"}, SourceRef{Path: "a"})
	chain := idx.Explain("name")
	if len(chain) != 1 {
		t.Fatalf("chain = %v", chain)
	}
	if chain[0].Value != nil {
		t.Errorf("TopLevel must not carry Value, got %v", chain[0].Value)
	}
}

// Slices are reference types; the recorded Value must be a clone so later
// mutation of the layer's slice cannot rewrite a held snapshot's origin.
func TestProvenance_SliceValueIsCloned(t *testing.T) {
	orig := []any{"a", "b"}
	idx := NewIndex(Full)
	idx.RecordTree("", map[string]any{"list": orig}, SourceRef{Path: "a"})
	orig[0] = "MUTATED"
	chain := idx.Explain("list")
	if len(chain) != 1 {
		t.Fatalf("chain = %v", chain)
	}
	got, ok := chain[0].Value.([]any)
	if !ok {
		t.Fatalf("Value type = %T", chain[0].Value)
	}
	if got[0] != "a" {
		t.Errorf("recorded slice value mutated through alias: %v", got)
	}
}

// A slice of containers must be captured deeply. The merge stage aliases layer
// subtrees into the merged tree and the secret, typed-hook and transform
// stages then rewrite that tree in place, so a shallow clone leaves the
// recorded layer value pointing at maps those stages mutate -- including
// resolved secret plaintext.
func TestProvenance_SliceOfMapsIsDeeplyCloned(t *testing.T) {
	inner := map[string]any{"name": "db", "token": "vault://ref"}
	orig := []any{inner}
	idx := NewIndex(Full)
	idx.RecordTree("", map[string]any{"items": orig}, SourceRef{Path: "a"})

	// Stand in for the secret stage rewriting the merged tree in place.
	inner["token"] = "PLAINTEXT"

	chain := idx.Explain("items")
	if len(chain) != 1 {
		t.Fatalf("chain = %v", chain)
	}
	got, ok := chain[0].Value.([]any)
	if !ok {
		t.Fatalf("Value type = %T", chain[0].Value)
	}
	m, ok := got[0].(map[string]any)
	if !ok {
		t.Fatalf("element type = %T", got[0])
	}
	if m["token"] != "vault://ref" {
		t.Errorf("recorded layer value mutated through alias: token = %v, want the pre-resolution reference", m["token"])
	}
}

// Nested slices are containers too.
func TestProvenance_NestedSliceIsDeeplyCloned(t *testing.T) {
	inner := []any{"a"}
	idx := NewIndex(Full)
	idx.RecordTree("", map[string]any{"rows": []any{inner}}, SourceRef{Path: "a"})
	inner[0] = "MUTATED"

	chain := idx.Explain("rows")
	if len(chain) != 1 {
		t.Fatalf("chain = %v", chain)
	}
	outer := chain[0].Value.([]any)
	row := outer[0].([]any)
	if row[0] != "a" {
		t.Errorf("recorded nested slice mutated through alias: %v", row)
	}
}

func TestProvenance_RecordingLevels(t *testing.T) {
	for _, level := range []Level{Off, TopLevel, Full} {
		idx := NewIndex(level)
		idx.RecordTree("", map[string]any{"nested": map[string]any{"value": "x"}, "list": []any{"y"}}, SourceRef{Path: "base"})
		idx.Record("nested.value", SourceRef{Path: "override"})
		if got := idx.Explain("missing"); got != nil {
			t.Fatalf("level %d: unknown path=%v", level, got)
		}
		switch level {
		case Off:
			if idx != nil || idx.Explain("list") != nil {
				t.Fatal("Off must not retain origins")
			}
		case TopLevel:
			if len(idx.Explain("nested")) != 1 || idx.Explain("nested.value") != nil || idx.Explain("list")[0].Value != nil {
				t.Fatal("TopLevel must record only top-level paths without values")
			}
		case Full:
			chain := idx.Explain("nested.value")
			if len(chain) != 2 || chain[0].Value != "x" || chain[1].Source.Path != "override" {
				t.Fatalf("Full must preserve layer order and values: %v", chain)
			}
		}
	}
}

func TestProvenance_ExplainReturnsIndependentValues(t *testing.T) {
	idx := NewIndex(Full)
	idx.RecordTree("", map[string]any{"items": []any{map[string]any{"token": "reference"}}}, SourceRef{Path: "base"})
	chain := idx.Explain("items")
	chain[0].Value.([]any)[0].(map[string]any)["token"] = "caller-mutation"
	chain[0].Source.Path = "caller-mutation"
	again := idx.Explain("items")
	if again[0].Source.Path != "base" || again[0].Value.([]any)[0].(map[string]any)["token"] != "reference" {
		t.Fatalf("Explain leaked retained origin data: %v", again)
	}
}

func TestLayerKindString(t *testing.T) {
	for kind, want := range map[LayerKind]string{
		LayerUnknown: "unknown", LayerMerge: "merge", LayerPatch: "patch",
		LayerProvider: "provider", LayerSecret: "secret", LayerGenerator: "generator",
		LayerOverride: "override", LayerKind(255): "unknown",
	} {
		if got := kind.String(); got != want {
			t.Errorf("kind %d: got %q, want %q", kind, got, want)
		}
	}
}
