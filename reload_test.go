package fastconf_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
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
		fastconf.WithProfile("prod"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	got := mgr.Get()
	if got.Database.DSN != "postgres://prod-patched" {
		t.Errorf("dsn = %q", got.Database.DSN)
	}
}

func TestPatchLayer_FailureKeepsOldState(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml":      &fstest.MapFile{Data: []byte("server:\n  addr: \":8080\"\n")},
		"conf.d/base/20-database.yaml": &fstest.MapFile{Data: []byte("database:\n  dsn: a\n  pool: 1\n")},
	}
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	gen1 := mgr.Snapshot().Generation

	mfs["conf.d/overlays/prod/99-bad.patch.yaml"] = &fstest.MapFile{Data: []byte(`
- op: remove
  path: /no/such/key
`)}
	mfs2 := fstest.MapFS{
		"c/base/00.yaml":                 &fstest.MapFile{Data: []byte("a: 1\n")},
		"c/overlays/p/99-bad.patch.yaml": &fstest.MapFile{Data: []byte("- op: remove\n  path: /missing\n")},
	}
	type tinyCfg struct {
		A int `yaml:"a" json:"a"`
	}
	_, err = fastconf.New[tinyCfg](context.Background(),
		fastconf.WithFS(mfs2), fastconf.WithDir("c"), fastconf.WithProfile("p"))
	if err == nil {
		t.Fatal("expected patch failure")
	}
	if mgr.Snapshot().Generation != gen1 {
		t.Errorf("unrelated manager mutated")
	}
}

func TestReloadLoopNoPendingAfterClose(t *testing.T) {
	// BUG-406: reloadLoop must NOT process any pending request after
	// m.closed fires, even when both channels are simultaneously ready.
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("v: 0\n")},
	}
	var reloadCount atomic.Int64
	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithValidator(func(m *map[string]any) error {
			reloadCount.Add(1)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Flood the reload channel, then close immediately.
	const burst = 50
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.Reload(context.Background()) // errors expected after close
		}()
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// wg.Wait is part of the regression: queued Reload callers must be
	// released by Close instead of hanging forever on req.doneCh.
	wg.Wait()

	frozen := reloadCount.Load()
	time.Sleep(10 * time.Millisecond)
	if after := reloadCount.Load(); after != frozen {
		t.Errorf("reloads continued after Close: count %d→%d", frozen, after)
	}
}

func TestReloadWithSource_PostMutationDoesNotAffectState(t *testing.T) {
	type cfg struct {
		K string `yaml:"k" json:"k"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("k: base\n")},
	}
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	override := map[string]any{"k": "v1"}
	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(override)); err != nil {
		t.Fatalf("ReloadWithSource: %v", err)
	}
	override["k"] = "v2"

	if got := mgr.Get().K; got != "v1" {
		t.Fatalf("post-mutation leaked into state: got %q", got)
	}
}

type rwsCfg struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

func TestReloadWithSource_Atomic(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 1\n")},
	}
	mgr, err := fastconf.New[rwsCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if mgr.Get().Port != 1 {
		t.Fatalf("initial port = %d", mgr.Get().Port)
	}
	gen0 := mgr.Snapshot().Generation

	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"port": 9999,
	})); err != nil {
		t.Fatalf("ReloadWithSource: %v", err)
	}
	if got := mgr.Get().Port; got != 9999 {
		t.Errorf("after override, port = %d", got)
	}
	if mgr.Get().Name != "base" {
		t.Errorf("name should be retained: %q", mgr.Get().Name)
	}
	if g := mgr.Snapshot().Generation; g != gen0+1 {
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

func TestReloadWithSource_NilFallsBackToReload(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 7\n")},
	}
	mgr, err := fastconf.New[rwsCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatalf("ReloadWithSource(nil): %v", err)
	}
	if mgr.Get().Port != 7 {
		t.Errorf("port should remain 7, got %d", mgr.Get().Port)
	}
}

// blockingProvider's Load blocks on <-ctx.Done() and returns ctx.Err()
// so we can prove that caller-side ctx threads into the pipeline.
type blockingProvider struct{ blocked chan struct{} }

func (p *blockingProvider) Name() string  { return "blocking" }
func (p *blockingProvider) Priority() int { return 100 }
func (p *blockingProvider) Load(ctx context.Context) (map[string]any, error) {
	if p.blocked != nil {
		select {
		case p.blocked <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *blockingProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

// TestReload_CallerCtxCancelsPipeline verifies P1.1: a caller-supplied ctx
// passed to Reload propagates into the running pipeline so a slow
// provider Load can be cancelled, not merely waited on.
func TestReload_CallerCtxCancelsPipeline(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("v: 0\n")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := fastconf.New[map[string]any](ctx,
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(&blockingProvider{}),
	)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("pipeline did not honour ctx promptly: %s", elapsed)
	}
}

// toggleProvider is a Provider whose Load behaviour flips between "fast
// successful return" and "block on ctx" via a sync/atomic flag. It lets
// us build a Manager whose initial reload succeeds and then drive a
// post-construction Reload(ctx) into a controlled slow path.
type toggleProvider struct {
	slow atomic.Bool
	data atomic.Pointer[map[string]any]
}

func (p *toggleProvider) Name() string  { return "toggle" }
func (p *toggleProvider) Priority() int { return 100 }
func (p *toggleProvider) Load(ctx context.Context) (map[string]any, error) {
	if p.slow.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d := p.data.Load(); d != nil {
		return *d, nil
	}
	return map[string]any{}, nil
}
func (p *toggleProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

// TestReload_PostConstructionCtxCancellation is the E2E counterpart to the
// initial-reload test above. It builds a healthy manager, flips a provider
// into slow mode, fires Reload(ctxWithTimeout) and asserts:
//
//  1. Reload returns context.DeadlineExceeded (not a wrapped ErrDecode).
//  2. Generation is unchanged — failure-safe contract holds.
//  3. Get() still returns the original value.
//  4. The failure event published on Errors() carries the ctx error so
//     fan-out consumers can errors.Is for the same sentinel.
//  5. After flipping back to fast mode, a manual Reload(ctx) succeeds and
//     advances Generation — i.e. one cancellation does not poison the loop.
func TestReload_PostConstructionCtxCancellation(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: initial\n")},
	}
	tp := &toggleProvider{}
	initial := map[string]any{"name": "initial"}
	tp.data.Store(&initial)

	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(tp),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	startGen := mgr.Snapshot().Generation
	startVal := (*mgr.Get())["name"]

	// Drain any pre-existing error events so we can wait for the new one.
	drained := false
	for !drained {
		select {
		case <-mgr.Errors():
		default:
			drained = true
		}
	}

	// Flip into slow mode and call Reload with a short deadline.
	tp.slow.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = mgr.Reload(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Reload err: want DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Reload did not honour ctx promptly: %s", elapsed)
	}
	if got := mgr.Snapshot().Generation; got != startGen {
		t.Errorf("Generation must not advance on failed reload; was %d, now %d", startGen, got)
	}
	if got := (*mgr.Get())["name"]; got != startVal {
		t.Errorf("live value mutated after failed reload: was %v, now %v", startVal, got)
	}

	// Errors() should publish exactly this failure with the same ctx sentinel.
	select {
	case re := <-mgr.Errors():
		if !errors.Is(re.Err, context.DeadlineExceeded) {
			t.Errorf("Errors channel: want wrap of DeadlineExceeded, got %v", re.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Errors channel did not receive the failure event")
	}

	// Recover: flip back to fast mode, perform an explicit Reload with a
	// fresh ctx. The single-writer loop must still be alive and progressive.
	tp.slow.Store(false)
	next := map[string]any{"name": "recovered"}
	tp.data.Store(&next)
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatalf("recovery reload failed: %v", err)
	}
	if got := mgr.Snapshot().Generation; got <= startGen {
		t.Errorf("Generation should advance after successful recovery reload; was %d, now %d", startGen, got)
	}
	if got := (*mgr.Get())["name"]; got != "recovered" {
		t.Errorf("recovery reload did not publish new value: got %v", got)
	}
}

func TestReloadWithSource_AfterCloseFails(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	mgr, _ := fastconf.New[rwsCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	mgr.Close()
	err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{"port": 1}))
	if !errors.Is(err, fastconf.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}
