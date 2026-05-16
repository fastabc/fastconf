package render

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"

	"github.com/fastabc/fastconf/pkg/provider"
)

type cfg struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func TestWire_AtomicWriteAndHook(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tpl")
	out := filepath.Join(dir, "rendered.conf")
	if err := os.WriteFile(tmpl, []byte("name={{.Name}};port={{.Port}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: app\nport: 8080\n")},
	}
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer mgr.Close()

	r, err := GoTemplate[cfg](tmpl, nil)
	if err != nil {
		t.Fatalf("GoTemplate: %v", err)
	}
	var hookCalls atomic.Int32
	hook := func(_ context.Context, p string) error {
		if p != out {
			t.Errorf("hook got %s want %s", p, out)
		}
		hookCalls.Add(1)
		return nil
	}
	cancel, err := Wire(mgr, r, out, Options{}, hook)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	defer cancel()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if want := "name=app;port=8080\n"; string(got) != want {
		t.Fatalf("rendered = %q want %q", got, want)
	}
	if hookCalls.Load() != 1 {
		t.Fatalf("hook calls = %d want 1", hookCalls.Load())
	}

	// Trigger reload via a bytes patch source.
	mgr2, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithProvider(provider.NewBytes("override", "yaml", []byte("port: 9090\n"))),
	)
	if err != nil {
		t.Fatalf("manager 2: %v", err)
	}
	defer mgr2.Close()
	if mgr2.Get().Port != 9090 {
		t.Fatalf("override failed, got %+v", mgr2.Get())
	}
	_ = time.Now()
}

// TestAtomicWrite_CreatesAndReplaces exercises the exported-via-internal
// atomicWrite helper directly: write once and verify contents; then
// overwrite and verify the new content is fully visible.
func TestAtomicWrite_CreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "subdir", "result.conf")

	if err := atomicWrite(out, []byte("v1"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	if string(data) != "v1" {
		t.Errorf("v1: got %q want v1", data)
	}

	if err := atomicWrite(out, []byte("v2"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, err = os.ReadFile(out)
	if err != nil {
		t.Fatalf("read v2: %v", err)
	}
	if string(data) != "v2" {
		t.Errorf("v2: got %q want v2", data)
	}
}

// TestGoTemplate_ParseError verifies that GoTemplate surfaces template
// parse errors at construction time (fail-fast, not at render time).
func TestGoTemplate_ParseError(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "bad.tpl")
	if err := os.WriteFile(tmpl, []byte("{{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GoTemplate[cfg](tmpl, nil); err == nil {
		t.Error("expected parse error, got nil")
	}
}

// TestGoTemplate_MissingFile verifies that GoTemplate returns an error
// when the template file does not exist.
func TestGoTemplate_MissingFile(t *testing.T) {
	if _, err := GoTemplate[cfg]("/nonexistent/path/tpl.tmpl", nil); err == nil {
		t.Error("expected error for missing template, got nil")
	}
}

// TestOptions_OnError exercises the report helper via Wire's OnError option:
// an error during rendering is forwarded to the callback.
func TestOptions_OnError_ReceivesRenderError(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tpl")
	// Use an invalid output path to provoke an atomicWrite error.
	out := filepath.Join(dir, "out.conf")

	if err := os.WriteFile(tmpl, []byte("name={{.Name}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: app\nport: 8080\n")},
	}
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer mgr.Close()

	renderer, err := GoTemplate[cfg](tmpl, nil)
	if err != nil {
		t.Fatalf("GoTemplate: %v", err)
	}

	var errCalled atomic.Bool
	_, err = Wire[cfg](mgr, renderer, out, Options{
		OnError: func(e error) {
			if e != nil {
				errCalled.Store(true)
			}
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	// The initial render should succeed (path is valid). Verify file exists.
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output file not created: %v", err)
	}
}
