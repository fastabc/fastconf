package testutil_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/internal/testutil"
)

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a", "b", "c.txt")
	testutil.WriteFile(t, p, "hello")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q want %q", data, "hello")
	}
}

func TestTempConf_CreatesFiles(t *testing.T) {
	root := testutil.TempConf(t, map[string]string{
		"conf.d/base/00.yaml":          "port: 8080\n",
		"conf.d/overlays/prod/01.yaml": "port: 443\n",
	})
	for _, rel := range []string{"conf.d/base/00.yaml", "conf.d/overlays/prod/01.yaml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestWaitFor_SuccessBeforeTimeout(t *testing.T) {
	var n atomic.Int32
	go func() {
		time.Sleep(50 * time.Millisecond)
		n.Store(1)
	}()
	testutil.WaitFor(t, func() bool { return n.Load() == 1 }, 3*time.Second, "counter never set")
}

func TestFakeProvider_LoadReturnsData(t *testing.T) {
	p := testutil.NewFakeProvider("fp", 100, map[string]any{"key": "val"})
	if p.Name() != "fp" {
		t.Errorf("name: got %q want fp", p.Name())
	}
	if p.Priority() != 100 {
		t.Errorf("priority: got %d want 100", p.Priority())
	}
	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data["key"] != "val" {
		t.Errorf("key: got %v want val", data["key"])
	}
}

func TestFakeProvider_WatchReturnsNil(t *testing.T) {
	p := testutil.NewFakeProvider("fp", 100, nil)
	ch, err := p.Watch(context.Background())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if ch != nil {
		t.Error("Watch: expected nil channel")
	}
}

func TestFakeProvider_LoadReturnsError(t *testing.T) {
	p := testutil.NewFakeProvider("fp", 100, nil)
	p.LoadErr = errors.New("boom")
	_, err := p.Load(context.Background())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("Load: want boom error, got %v", err)
	}
}

func TestFakeProvider_LoadReturnsCopy(t *testing.T) {
	orig := map[string]any{"k": "v"}
	p := testutil.NewFakeProvider("fp", 100, orig)
	data, _ := p.Load(context.Background())
	data["k"] = "tampered"
	data2, _ := p.Load(context.Background())
	if data2["k"] != "v" {
		t.Errorf("Load must return a copy; got %q after mutation", data2["k"])
	}
}

func TestRecordingTracer_CapturesSpans(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	ctx := context.Background()
	_, sp := tr.Start(ctx, "my-span")
	sp.SetAttribute("k", "v")
	sp.End()

	spans := tr.Spans()
	if len(spans) != 1 {
		t.Fatalf("Spans(): want 1, got %d", len(spans))
	}
	got := spans[0]
	if got.Name != "my-span" {
		t.Errorf("Name = %q, want my-span", got.Name)
	}
	if !got.Ended {
		t.Error("Ended should be true after End()")
	}
	if got.Attrs["k"] != "v" {
		t.Errorf("Attrs[k] = %v, want v", got.Attrs["k"])
	}
}

func TestRecordingTracer_FindSpan(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	ctx := context.Background()
	tr.Start(ctx, "alpha")
	tr.Start(ctx, "beta")

	sp := tr.FindSpan("beta")
	if sp == nil || sp.Name != "beta" {
		t.Fatalf("FindSpan('beta') = %v", sp)
	}
	if tr.FindSpan("missing") != nil {
		t.Error("FindSpan for unknown name should return nil")
	}
}

func TestRecordingSpan_RecordError(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	_, sp := tr.Start(context.Background(), "s")
	sp.RecordError(errors.New("oops"))

	spans := tr.Spans()
	if spans[0].Err == nil || spans[0].Err.Error() != "oops" {
		t.Errorf("Err = %v, want oops", spans[0].Err)
	}
}
