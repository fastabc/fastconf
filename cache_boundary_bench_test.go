package fastconf

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"
)

// Measure both the input shortcut and the full pipeline across cache capacity.
func BenchmarkReloadCacheBoundary(b *testing.B) {
	for _, n := range []int{512, 513} {
		for _, full := range []bool{false, true} {
			b.Run(fmt.Sprintf("layers=%d/full=%v", n, full), func(b *testing.B) {
				fs := fstest.MapFS{}
				for i := 0; i < n; i++ {
					fs[fmt.Sprintf("conf.d/base/%04d.yaml", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("key%d: %d\n", i, i))}
				}
				opts := []Option{WithFS(fs)}
				if full {
					opts = append(opts, WithTransform(func(map[string]any) error { return nil }))
				}
				m, err := New[map[string]any](context.Background(), opts...)
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { _ = m.Close() })
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := m.Reload(context.Background()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
