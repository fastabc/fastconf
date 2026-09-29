package render

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/internal/testutil"
)

type cfg struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func TestWire_AtomicWriteAndHook(t *testing.T) {
	dir := t.TempDir()
	templatePath, out := filepath.Join(dir, "template"), filepath.Join(dir, "rendered.conf")
	if err := os.WriteFile(templatePath, []byte("name={{.Name}};port={{.Port}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("name: app\nport: 8080\n")}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	renderer, err := GoTemplate[cfg](templatePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	cancel, err := Wire(mgr, renderer, out, Options{}, func(_ context.Context, path string) error {
		if path != out {
			t.Errorf("hook path=%q; want %q", path, out)
		}
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	for _, tc := range []struct {
		name     string
		override map[string]any
		want     string
		calls    int
	}{
		{"initial", nil, "name=app;port=8080\n", 1},
		{"reload", map[string]any{"port": 9090}, "name=app;port=9090\n", 2},
		{"unchanged", map[string]any{"port": 9090}, "name=app;port=9090\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.override != nil {
				if err := mgr.Reload(context.Background(), fastconf.WithOverride(tc.override)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want || calls != tc.calls {
				t.Fatalf("render=%q hooks=%d; want %q, %d", got, calls, tc.want, tc.calls)
			}
		})
	}
	cancel()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"port": 7070})); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("canceled render fired hook: %d", calls)
	}
}

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

func TestWire_OnErrorPreservesLastFile(t *testing.T) {
	for _, stage := range []string{"render", "hook"} {
		t.Run(stage, func(t *testing.T) {
			mgr := newVersionedManager(t)
			out := filepath.Join(t.TempDir(), "out")
			sentinel := errors.New(stage + " failed")
			var reported []error
			hookCalls := 0
			renderer := RendererFunc[versioned](func(v *versioned) ([]byte, error) {
				if stage == "render" && v.V == 2 {
					return nil, sentinel
				}
				return renderV(v)
			})
			cancel, err := Wire(mgr, renderer, out, Options{OnError: func(err error) { reported = append(reported, err) }}, func(context.Context, string) error {
				hookCalls++
				if stage == "hook" && hookCalls > 1 {
					return sentinel
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"v": 2})); err != nil {
				t.Fatal(err)
			}
			if len(reported) != 1 || !errors.Is(reported[0], sentinel) {
				t.Fatalf("reported=%v; want cause %v", reported, sentinel)
			}
			want := "2"
			if stage == "render" {
				want = "1"
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("file=%q; want %q", got, want)
			}
		})
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
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int32
	r := RendererFunc[versioned](func(v *versioned) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return renderV(v)
	})
	wired := make(chan func(), 1)
	go func() {
		cancel, err := Wire(mgr, r, out, Options{})
		if err != nil {
			t.Error(err)
		}
		wired <- cancel
	}()
	<-entered
	reloaded := make(chan error, 1)
	go func() { reloaded <- mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"v": 2})) }()
	testutil.WaitFor(t, func() bool { return mgr.Get().V == 2 }, 3*time.Second, "new snapshot committed")
	releaseOnce.Do(func() { close(release) })
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
