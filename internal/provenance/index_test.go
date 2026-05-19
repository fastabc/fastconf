package provenance

import (
	"testing"

	"github.com/fastabc/fastconf/internal/fctypes"
)

func TestProvenance_DepthGuard(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	idx := NewIndex(Full)
	idx.RecordTree("", m, fctypes.SourceRef{Path: "loop"})
	if idx == nil {
		t.Fatal("nil idx")
	}
}
