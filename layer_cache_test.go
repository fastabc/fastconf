package fastconf

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/scan"
)

type countingLayerCodec struct {
	calls *atomic.Int64
	value string
}

func (c countingLayerCodec) Decode(data []byte) (map[string]any, error) {
	c.calls.Add(1)
	return map[string]any{"nested": map[string]any{"value": c.value + string(data)}, "list": []any{map[string]any{"value": "original"}}}, nil
}

func TestFileLayerCacheInvalidation(t *testing.T) {
	var calls atomic.Int64
	const name = "layer-cache-test"
	codec.Register(name, countingLayerCodec{&calls, "old:"})
	codec.RegisterExt("cachetest", name)
	fs := fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{}, "conf.d/base/a.cachetest": &fstest.MapFile{Data: []byte("one")}}
	m, err := New[map[string]any](context.Background(), func(o *options) { o.FS = fs })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	scan := scan.ScanOptions{FS: fs}
	check := func(want string, count int64) {
		t.Helper()
		layers, err := m.assembleFileLayers(scan)
		if err != nil || len(layers) != 1 {
			t.Fatalf("layers=%v err=%v", layers, err)
		}
		raw := layers[0].data
		if got := raw["nested"].(map[string]any)["value"]; got != want {
			t.Fatalf("value=%v want=%v", got, want)
		}
		if got := raw["list"].([]any)[0].(map[string]any)["value"]; got != "original" {
			t.Fatalf("cached slice corrupted: %v", got)
		}
		raw["nested"].(map[string]any)["value"] = "mutated"
		raw["list"].([]any)[0].(map[string]any)["value"] = "mutated"
		if calls.Load() != count {
			t.Fatalf("decode calls=%d want=%d", calls.Load(), count)
		}
	}
	check("old:one", 1)
	check("old:one", 1)
	// Same size and mtime: contents, not metadata, determine invalidation.
	fs["conf.d/base/a.cachetest"].Data = []byte("two")
	check("old:two", 2)
	codec.Register(name, countingLayerCodec{&calls, "new:"})
	check("new:two", 3)
	delete(fs, "conf.d/base/a.cachetest")
	layers, err := m.assembleFileLayers(scan)
	if err != nil || len(layers) != 0 {
		t.Fatalf("deleted file: %v %v", layers, err)
	}
	fs["conf.d/base/a.cachetest"] = &fstest.MapFile{Data: []byte("two")}
	check("new:two", 3)
}

func TestLayerCacheBoundAndEviction(t *testing.T) {
	var calls atomic.Int64
	dec := countingLayerCodec{&calls, ""}
	var cache layerCache
	for i := 0; i < maxCachedLayers+10; i++ {
		layer := scan.Layer{Path: fmt.Sprint(i), Codec: "test", Bytes: []byte("original")}
		raw, err := cache.decoded(layer, sha256.Sum256(layer.Bytes), dec, 0)
		if err != nil || raw["nested"].(map[string]any)["value"] != "original" {
			t.Fatalf("decode %d: %v %v", i, raw, err)
		}
		if len(cache.entries) > maxCachedLayers || len(cache.keys) > maxCachedLayers {
			t.Fatal("cache exceeded bound")
		}
	}
	before := calls.Load()
	layer := scan.Layer{Path: "0", Codec: "test", Bytes: []byte("original")}
	if _, err := cache.decoded(layer, sha256.Sum256(layer.Bytes), dec, 0); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before+1 {
		t.Fatal("oldest layer was not evicted")
	}
	if _, err := cache.decoded(layer, sha256.Sum256(layer.Bytes), dec, 0); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before+1 {
		t.Fatal("reinserted layer missed cache")
	}
	// The codec name is independently part of the key.
	layer.Codec = "other"
	if _, err := cache.decoded(layer, sha256.Sum256(layer.Bytes), dec, 0); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before+2 {
		t.Fatal("codec name was omitted from key")
	}
}

func TestLayerCachePipelineMutationAndFailure(t *testing.T) {
	var fail bool
	rejected := errors.New("rejected")
	mutate := func(raw map[string]any) error {
		nested := raw["list"].([]any)[0].(map[string]any)
		nested["value"] = nested["value"].(string) + "!"
		if fail {
			return rejected
		}
		return nil
	}
	fs := fstest.MapFS{"conf.d/base/a.yaml": &fstest.MapFile{Data: []byte("list:\n  - value: original\n")}}
	m, err := New[map[string]any](context.Background(), func(o *options) { o.FS = fs; o.Transforms = []func(map[string]any) error{mutate} })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	original := m.Snapshot()
	fail = true
	if err := m.Reload(context.Background()); !errors.Is(err, rejected) {
		t.Fatalf("error=%v", err)
	}
	if m.Snapshot() != original {
		t.Fatal("failed reload published")
	}
	fail = false
	for i := 0; i < 3; i++ {
		plan, err := m.Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if plan.Proposed.Hash() != original.Hash() {
			t.Fatal("preview mutated cache")
		}
		if err := m.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
		if m.Snapshot() != original {
			t.Fatal("unchanged reload mutated cache")
		}
	}
	// A malformed file must fail every time and must not replace the snapshot.
	fs["conf.d/base/a.yaml"].Data = []byte("list: [")
	for i := 0; i < 2; i++ {
		if err := m.Reload(context.Background()); err == nil {
			t.Fatal("invalid file accepted")
		}
		if m.Snapshot() != original {
			t.Fatal("decode failure published")
		}
	}
	fs["conf.d/base/a.yaml"].Data = []byte("list:\n  - value: original\n")
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != original {
		t.Fatal("restoring file changed snapshot")
	}
}

func TestLayerCacheConcurrentRegistration(t *testing.T) {
	const name = "layer-cache-concurrent"
	var calls atomic.Int64
	codec.RegisterExt("cacheconcurrent", name)
	codec.Register(name, countingLayerCodec{&calls, "initial:"})
	m, err := New[map[string]any](context.Background(), func(o *options) {
		o.FS = fstest.MapFS{
			"conf.d/base/a.cacheconcurrent": &fstest.MapFile{Data: []byte("data")},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			codec.Register(name, countingLayerCodec{&calls, fmt.Sprint(i)})
		}
	}()
	for i := 0; i < 100; i++ {
		if err := m.Reload(context.Background()); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	codec.Register(name, countingLayerCodec{&calls, "final:"})
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*m.Get())["nested"].(map[string]any)["value"]; got != "final:data" {
		t.Fatalf("stale codec result: %v", got)
	}
}
