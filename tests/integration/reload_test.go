package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

func TestPatchLayer_AppliesRFC6902(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: \":8080\"\n")},
		"conf.d/base/20-database.yaml": &fstest.MapFile{Data: []byte(`
database:
  dsn: postgres://base
  pool: 10
`)},
		"conf.d/overlays/prod/30-database.patch.yaml": &fstest.MapFile{Data: []byte(`
- op: replace
  path: /database/dsn
  value: postgres://prod-patched
- op: add
  path: /database/replicas
  value: 3
`)},
	}
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	got := mgr.Get()
	if got.Database.DSN != "postgres://prod-patched" {
		t.Errorf("dsn = %q", got.Database.DSN)
	}
}

func TestPatchLayer_FailureKeepsOldState(t *testing.T) {
	fs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(fs), fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	before := mgr.Snapshot()
	fs["conf.d/overlays/prod/99-bad.patch.yaml"] = &fstest.MapFile{Data: []byte("- op: remove\n  path: /missing\n")}
	if err := mgr.Reload(context.Background()); !errors.Is(err, fastconf.ErrMerge) {
		t.Fatalf("Reload = %v; want ErrMerge", err)
	}
	if mgr.Snapshot() != before {
		t.Fatal("failed patch replaced the committed snapshot")
	}
	event := await(t, mgr.Errors())
	if !errors.Is(event.Err, fastconf.ErrMerge) {
		t.Fatalf("published error = %v; want ErrMerge", event.Err)
	}
}

type overrideConfig struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type overridePointerConfig struct {
	DB overrideDatabase `json:"db" yaml:"db"`
}

type overrideDatabase struct {
	DSN string `json:"dsn" yaml:"dsn"`
}

func TestReload_WithOverride_Atomic(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 1\n")},
	}
	mgr, err := fastconf.New[overrideConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if mgr.Get().Port != 1 {
		t.Fatalf("initial port = %d", mgr.Get().Port)
	}
	gen0 := mgr.Snapshot().Generation()

	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"port": 9999,
	})); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := mgr.Get().Port; got != 9999 {
		t.Errorf("after override, port = %d", got)
	}
	if mgr.Get().Name != "base" {
		t.Errorf("name should be retained: %q", mgr.Get().Name)
	}
	if g := mgr.Snapshot().Generation(); g != gen0+1 {
		t.Errorf("generation should have incremented; got %d (was %d)", g, gen0)
	}

	// A subsequent regular Reload must revert — the override is one-shot.
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := mgr.Get().Port; got != 1 {
		t.Errorf("after regular reload, port should revert to 1, got %d", got)
	}
}

func TestReload_WithOverride_NilFallsBackToReload(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 7\n")},
	}
	mgr, err := fastconf.New[overrideConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(nil)); err != nil {
		t.Fatalf("Reload(nil): %v", err)
	}
	if mgr.Get().Port != 7 {
		t.Errorf("port should remain 7, got %d", mgr.Get().Port)
	}
}

func TestReload_CancellationPreservesStateAndAllowsRecovery(t *testing.T) {
	p := &gateProvider{entered: make(chan struct{}, 1), release: make(chan struct{})}
	mgr, err := fastconf.New[overrideConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("name: initial\nport: 1\n")}}), fastconf.WithProvider(p))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	before := mgr.Snapshot()
	p.block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- mgr.Reload(ctx) }()
	await(t, p.entered)
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reload = %v; want context.Canceled", err)
	}
	if mgr.Snapshot() != before {
		t.Fatal("canceled reload published a snapshot")
	}
	if event := await(t, mgr.Errors()); !errors.Is(event.Err, context.Canceled) {
		t.Fatalf("published error = %v", event.Err)
	}
	p.block.Store(false)
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "recovered"})); err != nil {
		t.Fatal(err)
	}
	if mgr.Get().Name != "recovered" || mgr.Snapshot().Generation() != before.Generation()+1 {
		t.Fatal("reload did not recover after cancellation")
	}
}

func TestReload_WithOverride_AfterCloseFails(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	mgr, err := fastconf.New[overrideConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	err = mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"port": 1}))
	if !errors.Is(err, fastconf.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

// TestReload_WithOverride_DeepCopyAtCall verifies that WithOverride
// captures an independent copy, so callers can freely mutate the source map
// afterwards without racing the reload pipeline.
func TestReload_WithOverride_DeepCopyAtCall(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 80\n")},
	}
	mgr, err := fastconf.New[overrideConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	override := map[string]any{"name": "snapshot", "port": 99}
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(override)); err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's map after WithOverride; the snapshot
	// must reflect the value captured at call time.
	override["name"] = "mutated"
	override["port"] = 7777
	delete(override, "name")
	cfg := mgr.Get()
	if cfg.Name != "snapshot" {
		t.Errorf("WithOverride did not deep-copy: got Name=%q, want snapshot", cfg.Name)
	}
	if cfg.Port != 99 {
		t.Errorf("WithOverride did not deep-copy: got Port=%d, want 99", cfg.Port)
	}
}

func TestReload_WithOverride_DeepCopiesNestedPointerBeforePipeline(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("db:\n  dsn: base\n")},
	}
	gate := &gateProvider{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	mgr, err := fastconf.New[overridePointerConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(gate),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	overrideDB := &overrideDatabase{DSN: "snapshot"}
	gate.block.Store(true)
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
			"db": overrideDB,
		}))
	}()

	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not block reload")
	}
	overrideDB.DSN = "mutated"
	close(gate.release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Reload: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reload did not finish")
	}
	if got := mgr.Get().DB.DSN; got != "snapshot" {
		t.Fatalf("nested pointer override aliased caller mutation: got %q, want snapshot", got)
	}
}

func TestReload_WithOverride_InvalidOverrideReturnsDecodeError(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 80\n")},
	}
	mgr, err := fastconf.New[overrideConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	gen := mgr.Snapshot().Generation()
	err = mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"bad": func() {},
	}))
	if !errors.Is(err, fastconf.ErrDecode) {
		t.Fatalf("Reload err: want ErrDecode, got %v", err)
	}
	if got := mgr.Snapshot().Generation(); got != gen {
		t.Fatalf("invalid override advanced generation: got %d, want %d", got, gen)
	}
	if got := mgr.Get().Name; got != "base" {
		t.Fatalf("invalid override changed state: got name %q, want base", got)
	}
	select {
	case re := <-mgr.Errors():
		if re.Reason != "override" || !errors.Is(re.Err, fastconf.ErrDecode) {
			t.Fatalf("Errors entry = %+v, want override ErrDecode", re)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("invalid override did not publish to Errors")
	}
}

func TestReload_UnchangedInputKeepsGeneration(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	gen1 := mgr.Snapshot().Generation()
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen2 := mgr.Snapshot().Generation()
	if gen1 != gen2 {
		t.Errorf("generation should not change on identical reload: %d → %d", gen1, gen2)
	}
}

func TestReload_ChangedInputPublishesState(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	gen1 := mgr.Snapshot().Generation()
	mfs["conf.d/base/20-database.yaml"] = &fstest.MapFile{Data: []byte("database:\n  dsn: changed\n  pool: 99\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation should advance after content change")
	}
	if mgr.Get().Database.DSN != "changed" {
		t.Errorf("did not pick up new content")
	}
}

func TestReload_QueuedCallsFinishOnClose(t *testing.T) {
	var loads atomic.Int64
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithValidate(func(*appCfg) error { loads.Add(1); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	const burst = 50
	results := make(chan error, burst)
	start := make(chan struct{})
	for range burst {
		go func() { <-start; results <- mgr.Reload(context.Background()) }()
	}
	close(start)
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	for range burst {
		if err := await(t, results); err != nil && !errors.Is(err, fastconf.ErrClosed) {
			t.Fatalf("queued Reload = %v", err)
		}
	}
	frozen := loads.Load()
	if err := mgr.Reload(context.Background()); !errors.Is(err, fastconf.ErrClosed) {
		t.Fatalf("closed Reload = %v", err)
	}
	if loads.Load() != frozen {
		t.Fatal("closed manager invoked the pipeline")
	}
}

func TestCloseIdempotent(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Closing while invalid overrides are queued must release callers safely.
func TestClose_ConcurrentInvalidOverrides(t *testing.T) {
	for range 20 {
		mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mgr.Close() })
		const callers = 4
		start, results := make(chan struct{}), make(chan error, callers)
		for range callers {
			go func() {
				<-start
				results <- mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"bad": make(chan int)}))
			}()
		}
		close(start)
		if err := mgr.Close(); err != nil {
			t.Fatal(err)
		}
		for range callers {
			if err := await(t, results); !errors.Is(err, fastconf.ErrClosed) && !errors.Is(err, fastconf.ErrDecode) {
				t.Fatalf("Reload racing Close = %v", err)
			}
		}
	}
}

func TestShutdownCancelsInFlightRequests(t *testing.T) {
	for _, operation := range []string{"reload", "plan"} {
		t.Run(operation, func(t *testing.T) {
			p := &gateProvider{entered: make(chan struct{}, 1), release: make(chan struct{})}
			m, err := fastconf.New[map[string]any](context.Background(),
				fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			defer close(p.release)
			before := m.Snapshot()
			p.block.Store(true)
			done := make(chan error, 1)
			go func() {
				if operation == "plan" {
					_, err := m.Plan(context.Background())
					done <- err
				} else {
					done <- m.Reload(context.Background())
				}
			}()
			select {
			case <-p.entered:
			case <-time.After(time.Second):
				t.Fatal("provider was not called")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := m.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown failed to cancel cooperative provider: %v", err)
			}
			if err := <-done; !errors.Is(err, fastconf.ErrClosed) && !errors.Is(err, context.Canceled) {
				t.Fatalf("request error = %v", err)
			}
			if m.Snapshot() != before {
				t.Fatal("canceled request published a snapshot")
			}
		})
	}
}
