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
