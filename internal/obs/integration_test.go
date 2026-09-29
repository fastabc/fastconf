package obs_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/internal/testutil"
)

func TestTracerStageAttributes(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
		}),
		fastconf.WithTracer(tr),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	want := []struct {
		name string
		idx  int64
	}{
		{name: "merge", idx: 0},
		{name: "transform", idx: 1},
		{name: "secret", idx: 2},
		{name: "typed-hooks", idx: 3},
		{name: "decode", idx: 4},
		{name: "field-meta", idx: 5},
		{name: "validate", idx: 6},
		{name: "policy", idx: 7},
	}
	for _, tc := range want {
		sp := tr.FindSpan("fastconf." + tc.name)
		if sp == nil {
			t.Fatalf("missing span %q", tc.name)
		}
		if !sp.Ended {
			t.Errorf("span %q was not ended", tc.name)
		}
		if got := sp.Attrs["fastconf.stage"]; got != tc.name {
			t.Fatalf("span %q fastconf.stage = %v, want %q", tc.name, got, tc.name)
		}
		if got := sp.Attrs["fastconf.stage.index"]; got != tc.idx {
			t.Fatalf("span %q fastconf.stage.index = %v, want %d", tc.name, got, tc.idx)
		}
		if got := sp.Attrs["fastconf.stage.success"]; got != true {
			t.Fatalf("span %q fastconf.stage.success = %v, want true", tc.name, got)
		}
		if got := sp.Attrs["fastconf.reload.reason"]; got != "initial" {
			t.Fatalf("span %q fastconf.reload.reason = %v, want %q", tc.name, got, "initial")
		}
		elapsed, ok := sp.Attrs["fastconf.stage.elapsed_ms"].(int64)
		if !ok {
			t.Fatalf("span %q fastconf.stage.elapsed_ms type = %T, want int64", tc.name, sp.Attrs["fastconf.stage.elapsed_ms"])
		}
		if elapsed < 0 {
			t.Fatalf("span %q fastconf.stage.elapsed_ms = %d, want >= 0", tc.name, elapsed)
		}
	}
}
