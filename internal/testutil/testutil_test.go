package testutil_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
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

func TestWaitFor_PollsUntilSuccess(t *testing.T) {
	calls := 0
	testutil.WaitFor(t, func() bool { calls++; return calls == 3 }, time.Second, "condition was not polled")
	if calls != 3 {
		t.Fatalf("condition calls=%d; want 3", calls)
	}
}

func TestFakeProvider_LoadReturnsData(t *testing.T) {
	p := testutil.NewFakeProvider("fp", 100, map[string]any{"key": "val"})
	if p.Name() != "fp" {
		t.Errorf("name: got %q want fp", p.Name())
	}
	if contracts.Describe(p).Priority != 100 {
		t.Errorf("priority: got %d want 100", contracts.Describe(p).Priority)
	}
	snap, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data := snap.Map; data["key"] != "val" {
		t.Errorf("key: got %v want val", data["key"])
	}
}

func TestFakeProvider_LoadOnly(t *testing.T) {
	p := testutil.NewFakeProvider("fp", 100, nil)
	if _, ok := any(p).(contracts.Watcher); ok {
		t.Fatal("load-only provider unexpectedly implements Watcher")
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
	snap, _ := p.Load(context.Background())
	snap.Map["k"] = "tampered"
	snap2, _ := p.Load(context.Background())
	if snap2.Map["k"] != "v" {
		t.Errorf("Load must return a copy; got %q after mutation", snap2.Map["k"])
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
