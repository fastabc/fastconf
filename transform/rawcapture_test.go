package transform

import (
	"encoding/json"
	"testing"
)

func TestCaptureRaw_Basic(t *testing.T) {
	root := map[string]any{
		"listeners": []any{
			map[string]any{"name": "http", "port": 80},
		},
		"debug": true,
	}
	rc := CaptureRaw("listeners", "debug")
	if err := rc.Transform(root); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	raw, ok := rc.Get("listeners")
	if !ok {
		t.Fatal("expected listeners to be captured")
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal listeners: %v", err)
	}
	if len(got) != 1 || got[0]["name"] != "http" {
		t.Errorf("unexpected listeners value: %v", got)
	}

	debugRaw, ok := rc.Get("debug")
	if !ok {
		t.Fatal("expected debug to be captured")
	}
	var debugVal bool
	if err := json.Unmarshal(debugRaw, &debugVal); err != nil {
		t.Fatalf("unmarshal debug: %v", err)
	}
	if !debugVal {
		t.Error("debug should be true")
	}
}

func TestCaptureRaw_MissingPath(t *testing.T) {
	root := map[string]any{"other": 1}
	rc := CaptureRaw("listeners")
	if err := rc.Transform(root); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	_, ok := rc.Get("listeners")
	if ok {
		t.Error("missing path should not be present after reload")
	}
}

func TestCaptureRaw_All(t *testing.T) {
	root := map[string]any{"a": 1, "b": 2}
	rc := CaptureRaw("a", "b")
	if err := rc.Transform(root); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	all := rc.All()
	if len(all) != 2 {
		t.Errorf("expected 2 captured paths, got %d", len(all))
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := all[k]; !ok {
			t.Errorf("expected key %q in All()", k)
		}
	}
}

func TestCaptureRaw_ConcurrentReadWrite(t *testing.T) {
	rc := CaptureRaw("x")
	root := map[string]any{"x": 42}
	// Run writes and reads concurrently to trigger race detector.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			_ = rc.Transform(root)
		}
	}()
	for range 100 {
		rc.All()
		rc.Get("x")
	}
	<-done
}
