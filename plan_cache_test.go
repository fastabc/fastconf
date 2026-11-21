package fastconf

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/codec"
)

func planCacheValue(t *testing.T, v *map[string]any) any {
	t.Helper()
	nested, ok := (*v)["nested"].(map[string]any)
	if !ok {
		t.Fatalf("missing nested value: %v", *v)
	}
	return nested["value"]
}

// A Plan with a hypothetical hostname scans a different axis directory. It
// must not evict the live reload's cached layers, so the next Reload decodes
// nothing new. Before the fix, Plan trimmed the cache to its own scan and the
// live host layer was decoded again.
func TestPlanHostnameKeepsLiveLayerCache(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	target := host + "-preview"
	var calls atomic.Int64
	const name = "plan-cache-test"
	codec.Register(name, countingLayerCodec{&calls, "p:"})
	codec.RegisterExt("plancache", name)
	fs := fstest.MapFS{
		"conf.d/base/00.plancache":                 {Data: []byte("base")},
		"conf.d/hosts/" + host + "/00.plancache":   {Data: []byte("local")},
		"conf.d/hosts/" + target + "/00.plancache": {Data: []byte("target")},
	}
	m, err := New[map[string]any](context.Background(), WithFS(fs),
		WithAxes(Axis{Dir: "hosts", FromHostname: true}),
		// A validator disables the unchanged-input shortcut, so every Reload
		// decodes through the layer cache.
		WithValidate(func(*map[string]any) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	expectCalls := func(want int64, what string) {
		t.Helper()
		if got := calls.Load(); got != want {
			t.Fatalf("%s: decode calls = %d; want %d", what, got, want)
		}
	}
	expectCalls(2, "initial load (base + live host)")
	if got := planCacheValue(t, m.Get()); got != "p:local" {
		t.Fatalf("live value = %v; want p:local", got)
	}

	for i := range 2 {
		plan, err := m.Plan(context.Background(), WithPlanHostname(target))
		if err != nil {
			t.Fatal(err)
		}
		if got := planCacheValue(t, plan.Proposed.Value()); got != "p:target" {
			t.Fatalf("plan #%d value = %v; want p:target", i, got)
		}
		// The first preview decodes only the target file; the second hits the
		// entry the first admitted.
		expectCalls(3, "preview with target hostname")
	}

	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectCalls(3, "live reload after preview")
	if got := planCacheValue(t, m.Get()); got != "p:local" {
		t.Fatalf("live value after reload = %v; want p:local", got)
	}

	// The live reload trims the cache to its own files, so preview entries stay
	// bounded by the next live reload.
	if _, err := m.Plan(context.Background(), WithPlanHostname(target)); err != nil {
		t.Fatal(err)
	}
	expectCalls(4, "preview after live reload")
}
