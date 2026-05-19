//go:build fastconf_otel

package fastconf

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/internal/testutil"
)

func TestOTELRunStagesEnrichesStageSpans(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	mgr, err := New[map[string]any](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
		}),
		WithTracer(tr),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	want := []struct {
		name string
		idx  int64
	}{
		{name: "merge", idx: 0},
		{name: "migration", idx: 1},
		{name: "transform", idx: 2},
		{name: "secret", idx: 3},
		{name: "typed-hooks", idx: 4},
		{name: "decode", idx: 5},
		{name: "field-meta", idx: 6},
		{name: "validate", idx: 7},
		{name: "policy", idx: 8},
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
