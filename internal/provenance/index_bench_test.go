package provenance

import (
	"fmt"
	"strings"
	"testing"
)

var benchOriginsSink []Origin

func BenchmarkExplainDeep(b *testing.B) {
	idx := NewIndex(Full)
	path := strings.Repeat("node.", 31) + "leaf"
	for i := 0; i < 16; i++ {
		idx.Record(path, SourceRef{Path: fmt.Sprintf("layer-%02d", i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchOriginsSink = idx.Explain(path)
	}
}
