package fastconf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

func newPanicFixture(t *testing.T, opts ...Option) *Manager[snapshotConfig] {
	t.Helper()
	base := []Option{WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`)))}
	m, err := New[snapshotConfig](context.Background(), append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func nextReloadError(t *testing.T, ch <-chan ReloadError) ReloadError {
	t.Helper()
	select {
	case re := <-ch:
		return re
	case <-time.After(5 * time.Second):
		t.Fatal("no Errors entry")
		return ReloadError{}
	}
}

// A panic in user code on the writer becomes a reload error: the published
// snapshot stays, the writer survives and later reloads still run.
func TestWriterPanicBecomesReloadError(t *testing.T) {
	var calls atomic.Int32
	m := newPanicFixture(t, WithValidate(func(*snapshotConfig) error {
		if calls.Add(1) == 2 {
			panic("validator exploded")
		}
		return nil
	}))
	gen := m.Snapshot().Generation()

	err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"}))
	if !errors.Is(err, ErrPanic) || !errors.Is(err, ErrFastConf) {
		t.Fatalf("Reload = %v; want ErrPanic", err)
	}
	for _, want := range []string{"validate", "validator exploded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if got := m.Snapshot().Generation(); got != gen {
		t.Fatalf("generation = %d after panic; want %d", got, gen)
	}
	if re := nextReloadError(t, m.Errors()); !errors.Is(re.Err, ErrPanic) || re.Trigger != TriggerManual {
		t.Fatalf("Errors entry = %+v; want ErrPanic under manual", re)
	}
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "gamma"})); err != nil {
		t.Fatalf("Reload after panic = %v", err)
	}
	if got := m.Snapshot().Value().Name; got != "gamma" {
		t.Fatalf("name = %q after recovery; want gamma", got)
	}
}

// Work queued with run (rollback, plan, option errors) is covered as well.
func TestWriterPanicInQueuedRun(t *testing.T) {
	m := newPanicFixture(t)
	err := m.submitAndWait(context.Background(), reloadRequest{
		trigger: TriggerManual,
		reason:  "manual",
		run:     func(context.Context) error { panic("run exploded") },
	})
	if !errors.Is(err, ErrPanic) || !strings.Contains(err.Error(), "run exploded") {
		t.Fatalf("run panic = %v; want ErrPanic with value", err)
	}
	if re := nextReloadError(t, m.Errors()); !errors.Is(re.Err, ErrPanic) {
		t.Fatalf("Errors entry = %v; want ErrPanic", re.Err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload after panic = %v", err)
	}
}

// A transform panic during a file-triggered reload reaches Errors and leaves
// the writer able to run the next Reload.
func TestFileReloadPanicReachesErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base", "00.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var boom atomic.Bool
	m, err := New[snapshotConfig](context.Background(), WithDir(dir),
		WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}),
		WithTransform(func(map[string]any) error {
			if boom.Load() {
				panic("transform exploded")
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	boom.Store(true)
	if err := os.WriteFile(path, []byte("name: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	re := nextReloadError(t, m.Errors())
	if !errors.Is(re.Err, ErrPanic) || re.Trigger != TriggerFile || !strings.Contains(re.Err.Error(), "transform") {
		t.Fatalf("Errors entry = %+v; want ErrPanic in transform under file", re)
	}
	boom.Store(false)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload after panic = %v", err)
	}
	if got := m.Snapshot().Value().Name; got != "b" {
		t.Fatalf("name = %q; want b", got)
	}
}

// Observers do not take part in publication: a panicking observer is logged
// and the reload still commits.
func TestObserverPanicDoesNotAbortReload(t *testing.T) {
	var committed atomic.Int32
	m := newPanicFixture(t,
		WithObserver(observerFunc(func(_ context.Context, e Event) {
			if _, ok := e.(ReloadStarted); ok {
				panic("observer exploded")
			}
		}), observerFunc(func(_ context.Context, e Event) {
			if _, ok := e.(Committed); ok {
				committed.Add(1)
			}
		})))
	gen := m.Snapshot().Generation()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatalf("Reload = %v; want commit despite observer panic", err)
	}
	if got := m.Snapshot().Generation(); got != gen+1 {
		t.Fatalf("generation = %d; want %d", got, gen+1)
	}
	// The later observer still sees every commit, including the initial one.
	if got := committed.Load(); got != 2 {
		t.Fatalf("second observer saw %d commits; want 2", got)
	}
}

// The initial load recovers too, so New returns the panic as an error.
func TestInitialLoadPanicReturnsError(t *testing.T) {
	_, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`))),
		WithTransform(func(map[string]any) error { panic("initial exploded") }))
	if !errors.Is(err, ErrPanic) || !strings.Contains(err.Error(), "initial exploded") {
		t.Fatalf("New = %v; want ErrPanic", err)
	}
}
