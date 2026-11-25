package integration_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
)

// Each load produces a different port so the test can observe a committed reload.
type fakeWatcherProvider struct {
	name    string
	loadCnt atomic.Int64
	ch      chan contracts.Event
}

func (f *fakeWatcherProvider) Name() string { return f.name }

func (f *fakeWatcherProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}

func (f *fakeWatcherProvider) Load(_ context.Context) (contracts.Snapshot, error) {
	n := f.loadCnt.Add(1)
	return contracts.Snapshot{Map: map[string]any{"server": map[string]any{"port": int(8000 + n)}}}, nil
}

func (f *fakeWatcherProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return f.ch, nil
}

type pwCfg struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
}

type recordingProviderMetrics struct {
	dropped atomic.Int64
	errors  atomic.Int64
}

func (m *recordingProviderMetrics) Observe(_ context.Context, e fastconf.Event) {
	switch e.(type) {
	case fastconf.ProviderError:
		m.errors.Add(1)
	case fastconf.EventDropped:
		m.dropped.Add(1)
	}
}

type blockingWatcherProvider struct {
	name              string
	loadCnt           atomic.Int64
	ch                chan contracts.Event
	secondLoadStarted chan struct{}
	releaseSecondLoad chan struct{}
}

func (p *blockingWatcherProvider) Name() string { return p.name }

func (p *blockingWatcherProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}

func (p *blockingWatcherProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	n := p.loadCnt.Add(1)
	if n == 2 {
		close(p.secondLoadStarted)
		select {
		case <-p.releaseSecondLoad:
		case <-ctx.Done():
			return contracts.Snapshot{}, ctx.Err()
		}
	}
	return contracts.Snapshot{Map: map[string]any{"server": map[string]any{"port": int(9000 + n)}}}, nil
}

func (p *blockingWatcherProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return p.ch, nil
}

func TestProviderWatch_TriggersReload(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fileWatch bool
	}{
		{"with file watch", true},
		{"without file watch", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// MapFS isolates provider events from filesystem notifications.
			fs := fstest.MapFS{"conf.d/base/00-app.yaml": {Data: []byte("server: {port: 7777}")}}
			p := &fakeWatcherProvider{name: "pw", ch: make(chan contracts.Event)}
			opts := []fastconf.Option{fastconf.WithFS(fs), fastconf.WithProvider(p)}
			if tc.fileWatch {
				opts = append(opts, fastconf.WithWatch(fastconf.Watch{}))
			}
			cfg, err := fastconf.New[pwCfg](context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cfg.Close() }()

			before := cfg.Snapshot().Generation()
			if got := cfg.Get().Server.Port; got != 8001 {
				t.Fatalf("initial port = %d, want 8001", got)
			}
			sendProviderEvent(t, p.ch, contracts.Event{Source: p.name, Reason: "tick"})
			waitFor(t, func() bool { return cfg.Snapshot().Generation() > before }, "provider event reload")
			if got := cfg.Get().Server.Port; got != 8002 {
				t.Fatalf("port after event = %d, want 8002", got)
			}
		})
	}
}

func TestProviderWatch_PauseSkipsProviderReload(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	p := &fakeWatcherProvider{name: "paused", ch: make(chan contracts.Event)}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.Watch{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()

	gen0 := cfg.Snapshot().Generation()
	load0 := p.loadCnt.Load()
	cfg.Pause()
	if !cfg.Paused() {
		t.Fatal("watcher should report paused")
	}

	sendProviderEvent(t, p.ch, contracts.Event{Source: "paused", Reason: "ignored"})
	time.Sleep(150 * time.Millisecond)
	if got := p.loadCnt.Load(); got != load0 {
		t.Fatalf("paused provider event triggered Load: got %d want %d", got, load0)
	}
	if got := cfg.Snapshot().Generation(); got != gen0 {
		t.Fatalf("paused provider event advanced generation: got %d want %d", got, gen0)
	}

	cfg.Resume()
	sendProviderEvent(t, p.ch, contracts.Event{Source: "paused", Reason: "resumed"})
	waitFor(t, func() bool { return cfg.Snapshot().Generation() > gen0 }, "provider reload after resume")
}

func TestProviderWatch_EventDroppedWhenReloadQueueFull(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	metrics := &recordingProviderMetrics{}
	p := &blockingWatcherProvider{
		name:              "blocking",
		ch:                make(chan contracts.Event),
		secondLoadStarted: make(chan struct{}),
		releaseSecondLoad: make(chan struct{}),
	}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.Watch{}),
		fastconf.WithObserver(metrics),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	release := sync.OnceFunc(func() { close(p.releaseSecondLoad) })
	defer release()

	before := cfg.Snapshot().Generation()
	sendProviderEvent(t, p.ch, contracts.Event{Source: p.name, Reason: "block"})
	select {
	case <-p.secondLoadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second load never started")
	}

	// The unbuffered channel requires the watcher to consume the entire burst
	// while the blocked load keeps the reload queue from draining.
	for range 64 {
		sendProviderEvent(t, p.ch, contracts.Event{Source: p.name, Reason: "burst"})
	}
	waitFor(t, func() bool { return metrics.dropped.Load() > 0 }, "reload queue overflow event")

	release()
	waitFor(t, func() bool { return cfg.Snapshot().Generation() >= before+2 }, "commit from queued burst after blocked load")
}

func sendProviderEvent(t *testing.T, ch chan contracts.Event, ev contracts.Event) {
	t.Helper()
	select {
	case ch <- ev:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out sending provider event %q", ev.Reason)
	}
}

type closeWatchProvider struct {
	mu      sync.Mutex
	ctx     context.Context
	started chan struct{}
}

func (p *closeWatchProvider) Name() string { return "close-watch" }

func (p *closeWatchProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}

func (p *closeWatchProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"ok": true}}, nil
}

func (p *closeWatchProvider) Watch(ctx context.Context, _ string) (<-chan contracts.Event, error) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	return make(chan contracts.Event), nil
}

func TestCloseCancelsProviderWatchContext(t *testing.T) {
	p := &closeWatchProvider{started: make(chan struct{})}
	m, err := fastconf.New[map[string]any](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{}`)}}), fastconf.WithProvider(p), fastconf.WithWatch(fastconf.Watch{}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("watch did not start")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	ctx := p.ctx
	p.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel provider context")
	}
}

// contractProvider implements the v1 Provider contract: Load returns a
// Snapshot, Watch receives the last observed revision, and Describe carries
// the priority as metadata.
type contractProvider struct {
	name  string
	prio  int
	port  int
	mu    sync.Mutex
	froms []string
	chs   []chan contracts.Event
}

func (p *contractProvider) Name() string { return p.name }

func (p *contractProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.prio}
}

func (p *contractProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"port": p.port}, Revision: "r0"}, nil
}

func (p *contractProvider) Watch(_ context.Context, from string) (<-chan contracts.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.froms = append(p.froms, from)
	ch := make(chan contracts.Event, 1)
	p.chs = append(p.chs, ch)
	return ch, nil
}

func (p *contractProvider) watchCalls() ([]string, []chan contracts.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.froms...), append([]chan contracts.Event(nil), p.chs...)
}

// TestProvider_WatchResumesFromLastRevision verifies: the manager
// resubscribes with the last revision it saw; the first subscribe is cold.
func TestProvider_WatchResumesFromLastRevision(t *testing.T) {
	p := &contractProvider{name: "resume", prio: contracts.PriorityKV, port: 1}
	mgr, err := fastconf.New[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	var chs []chan contracts.Event
	waitFor(t, func() bool { _, chs = p.watchCalls(); return len(chs) == 1 }, "first Watch")
	chs[0] <- contracts.Event{Reason: "tick", Revision: "r7", At: time.Now()}
	close(chs[0])
	var froms []string
	waitFor(t, func() bool { froms, _ = p.watchCalls(); return len(froms) == 2 }, "resubscribe")
	if froms[0] != "" || froms[1] != "r7" {
		t.Fatalf("Watch from = %q; want [\"\" \"r7\"]", froms)
	}
}

// TestProvider_GapEventCountsAsProviderError verifies: a provider that
// could not resume reports Event.Gap, which the manager records as a
// provider error and still reloads.
func TestProvider_GapEventCountsAsProviderError(t *testing.T) {
	metrics := &recordingProviderMetrics{}
	p := &contractProvider{name: "gap", prio: contracts.PriorityKV, port: 1}
	mgr, err := fastconf.New[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(p), fastconf.WithObserver(metrics))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	var chs []chan contracts.Event
	waitFor(t, func() bool { _, chs = p.watchCalls(); return len(chs) == 1 }, "first Watch")
	gen := mgr.Snapshot().Generation()
	p.port = 3
	chs[0] <- contracts.Event{Reason: "resubscribed", Gap: true, At: time.Now()}
	waitFor(t, func() bool { return mgr.Snapshot().Generation() > gen }, "reload after gap event")
	if metrics.errors.Load() != 1 {
		t.Fatalf("provider errors = %d; want 1 for the gap", metrics.errors.Load())
	}
}
