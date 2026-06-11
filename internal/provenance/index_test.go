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
