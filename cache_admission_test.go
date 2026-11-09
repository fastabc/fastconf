package fastconf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/scan"
)

func TestLayerCacheWorkingSetExceedsCapacity(t *testing.T) {
	var calls atomic.Int64
	const name = "cache-working-set"
	codec.Register(name, countingLayerCodec{&calls, ""})
	codec.RegisterExt(name, name)
	fs := fstest.MapFS{}
	for i := 0; i < maxCachedLayers+1; i++ {
		fs[fmt.Sprintf("conf.d/base/%04d.%s", i, name)] = &fstest.MapFile{Data: []byte("value")}
	}
	m, err := New[map[string]any](context.Background(), WithFS(fs), WithTransform(func(map[string]any) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	for range 3 {
		calls.Store(0)
		if err := m.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("full reload decoded %d files; want only the uncached layer", got)
		}
	}
	// A vanished path releases its slot, and the formerly uncached layer is admitted.
	delete(fs, "conf.d/base/0000."+name)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	if _, err := m.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("vacated slot was not reused: %d decodes", got)
	}
}

type sizeOnlyCodec struct{}

func (sizeOnlyCodec) Decode(data []byte) (map[string]any, error) {
	return map[string]any{"size": len(data)}, nil
}

func TestLayerCacheSourceBudget(t *testing.T) {
	var cache layerCache
	load := func(path string, size int) *decodedLayer {
		t.Helper()
		l := scan.Layer{Path: path, Codec: "size", Bytes: make([]byte, size)}
		e, err := cache.getOrDecode(l, sha256.Sum256(l.Bytes), sizeOnlyCodec{}, 1)
		if err != nil || e.tree["size"] != size {
			t.Fatalf("load: %v, %v", e, err)
		}
		if cache.sourceBytes > maxCachedSourceBytes {
			t.Fatal("source-byte budget exceeded")
		}
		return e
	}
	load("first", maxCachedSourceBytes/2)
	load("second", maxCachedSourceBytes/2)
	load("overflow", 1)
	if len(cache.entries) != 2 || cache.sourceBytes != maxCachedSourceBytes {
		t.Fatal("budget did not limit admission")
	}
	// Growing an existing entry must release its obsolete cached version.
	load("first", maxCachedSourceBytes+1)
	if len(cache.entries) != 1 || cache.sourceBytes != maxCachedSourceBytes/2 {
		t.Fatal("oversized replacement retained old cache data")
	}
	load("third", 1)
	cache.retain(nil)
	if len(cache.entries) != 0 || cache.sourceBytes != 0 {
		t.Fatal("removed paths retained budget")
	}
}
