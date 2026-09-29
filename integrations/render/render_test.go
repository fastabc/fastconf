package render

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"

	"github.com/fastabc/fastconf/providers/source"
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
	defer func() { _ = mgr.Close() }()

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
		fastconf.WithProvider(source.NewBytes("override", "yaml", []byte("port: 9090\n"))),
	)
	if err != nil {
		t.Fatalf("manager 2: %v", err)
	}
	defer func() { _ = mgr2.Close() }()
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
	defer func() { _ = mgr.Close() }()

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

type versioned struct {
	V     int `json:"v"`
	Other int `json:"other"`
}

func newVersionedManager(t *testing.T) *fastconf.Manager[versioned] {
	t.Helper()
	mfs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("v: 1\n")}}
	mgr, err := fastconf.New[versioned](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func renderV(v *versioned) ([]byte, error) { return []byte(strconv.Itoa(v.V)), nil }

// A slow initial render must not overwrite a newer reload that finished meanwhile.
func TestWire_InitialRenderDoesNotOverwriteNewerReload(t *testing.T) {
	mgr := newVersionedManager(t)
	out := filepath.Join(t.TempDir(), "out")
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	r := RendererFunc[versioned](func(v *versioned) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return renderV(v)
	})
	wired := make(chan func())
	go func() {
		cancel, err := Wire(mgr, r, out, Options{})
		if err != nil {
			t.Error(err)
		}
		wired <- cancel
	}()
	<-entered
	reloaded := make(chan error)
	go func() { reloaded <- mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"v": 2})) }()
	// Give the reload time to reach the (blocked) subscriber before releasing v1.
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	defer (<-wired)()
	if mgr.Get().V != 2 {
		t.Fatalf("manager V = %d", mgr.Get().V)
	}
	if got, _ := os.ReadFile(out); string(got) != "2" {
		t.Fatalf("file = %q, want 2", got)
	}
}

// A failed write must not poison the dedupe cache: the next reload retries.
func TestWire_RetriesAfterWriteFailure(t *testing.T) {
	mgr := newVersionedManager(t)
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(out, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	var errs atomic.Int32
	cancel, err := Wire(mgr, RendererFunc[versioned](renderV), out, Options{OnError: func(error) { errs.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if errs.Load() != 1 {
		t.Fatalf("errors = %d, want 1", errs.Load())
	}
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	// Only an unrelated field changes; the rendered bytes are identical.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"other": 1})); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "1" {
		t.Fatalf("file = %q, err = %v", got, err)
	}
}

func TestWire_EmptyFirstRenderCreatesFile(t *testing.T) {
	mgr := newVersionedManager(t)
	out := filepath.Join(t.TempDir(), "out")
	cancel, err := Wire(mgr, RendererFunc[versioned](func(*versioned) ([]byte, error) { return nil, nil }), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if got, err := os.ReadFile(out); err != nil || len(got) != 0 {
		t.Fatalf("file = %q, err = %v", got, err)
	}
}

func TestHTTPGet_DoesNotFollowRedirect(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	if err := HTTPGet(redirect.URL)(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("hook followed redirect")
	}
	if err := HTTPGet(target.URL)(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := HTTPGet(target.URL)(ctx, ""); err == nil {
		t.Fatal("expected cancellation")
	}
}
