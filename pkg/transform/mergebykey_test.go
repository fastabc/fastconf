package transform

import (
	"encoding/json"
	"testing"
)

func TestMergeByKey_Basic(t *testing.T) {
	root := map[string]any{
		"listeners": []any{
			map[string]any{"name": "http", "port": 80},
			map[string]any{"name": "https", "port": 443},
		},
	}
	tr := MergeByKey("listeners", "name")
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform: %v", err)
	}
	items := root["listeners"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestMergeByKey_MergesDuplicates(t *testing.T) {
	root := map[string]any{
		"listeners": []any{
			map[string]any{"name": "http", "port": 80, "timeout": 30},
			map[string]any{"name": "https", "port": 443},
			map[string]any{"name": "http", "port": 8080}, // overlay: updates port
		},
	}
	tr := MergeByKey("listeners", "name")
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform: %v", err)
	}
	items := root["listeners"].([]any)
	// Should have 2 unique entries (http, https).
	if len(items) != 2 {
		t.Fatalf("expected 2 items after merge, got %d", len(items))
	}
	for _, item := range items {
		m := item.(map[string]any)
		if m["name"] == "http" {
			if m["port"] != 8080 {
				t.Errorf("http port: got %v, want 8080", m["port"])
			}
			if m["timeout"] != 30 {
				t.Errorf("http timeout: got %v, want 30 (should be preserved)", m["timeout"])
			}
		}
	}
}

func TestMergeByKey_MissingPath(t *testing.T) {
	root := map[string]any{}
	tr := MergeByKey("nonexistent", "name")
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform on missing path: %v", err)
	}
}

func TestMergeByKey_NonArrayPath(t *testing.T) {
	root := map[string]any{"listeners": "not-an-array"}
	tr := MergeByKey("listeners", "name")
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform on non-array: %v", err)
	}
}

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
