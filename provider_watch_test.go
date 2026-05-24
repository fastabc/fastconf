package fastconf_test

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

// fakeWatcherProvider emits a stream of events on demand to test the
// Provider Watch wiring: events drive reloads through the serialized
// reloadCh, with bounded queue and drop-on-overflow semantics.
type fakeWatcherProvider struct {
	name     string
	priority int
	loadCnt  atomic.Int64
	ch       chan contracts.Event
	once     sync.Once
}

func (f *fakeWatcherProvider) Name() string  { return f.name }
func (f *fakeWatcherProvider) Priority() int { return f.priority }
func (f *fakeWatcherProvider) Load(_ context.Context) (map[string]any, error) {
	n := f.loadCnt.Add(1)
	return map[string]any{"server": map[string]any{"port": int(8000 + n)}}, nil
}
func (f *fakeWatcherProvider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	f.once.Do(func() {
		if f.ch == nil {
			f.ch = make(chan contracts.Event, 4)
		}
	})
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

func (*recordingProviderMetrics) ReloadStarted()                     {}
func (*recordingProviderMetrics) ReloadFinished(bool, time.Duration) {}
func (*recordingProviderMetrics) StateGeneration(uint64)             {}
func (*recordingProviderMetrics) LayersTotal(int)                    {}
func (m *recordingProviderMetrics) ProviderError(string)             { m.errors.Add(1) }
func (m *recordingProviderMetrics) EventDropped(string)              { m.dropped.Add(1) }

type blockingWatcherProvider struct {
	name              string
	loadCnt           atomic.Int64
	ch                chan contracts.Event
	secondLoadStarted chan struct{}
	releaseSecondLoad chan struct{}
	startOnce         sync.Once
}

func (p *blockingWatcherProvider) Name() string  { return p.name }
func (p *blockingWatcherProvider) Priority() int { return contracts.PriorityKV }
func (p *blockingWatcherProvider) Load(ctx context.Context) (map[string]any, error) {
	n := p.loadCnt.Add(1)
	if n == 2 {
		p.startOnce.Do(func() { close(p.secondLoadStarted) })
		select {
		case <-p.releaseSecondLoad:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return map[string]any{"server": map[string]any{"port": int(9000 + n)}}, nil
}
func (p *blockingWatcherProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return p.ch, nil
}

type fallbackResumableProvider struct {
	name           string
	mu             sync.Mutex
	watchCalls     int
	watchFromCalls int
	ch1            chan contracts.Event
	ch2            chan contracts.Event
}

func (p *fallbackResumableProvider) Name() string  { return p.name }
func (p *fallbackResumableProvider) Priority() int { return contracts.PriorityKV }
func (p *fallbackResumableProvider) Load(context.Context) (map[string]any, error) {
	return map[string]any{"server": map[string]any{"port": 7001}}, nil
}
func (p *fallbackResumableProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchCalls++
	if p.watchCalls == 1 {
		return p.ch1, nil
	}
	return p.ch2, nil
}
func (p *fallbackResumableProvider) WatchFrom(context.Context, string) (<-chan contracts.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchFromCalls++
	return nil, contracts.ErrResumeUnsupported
}

type successfulResumableProvider struct {
	name          string
	mu            sync.Mutex
	watchCalls    int
	watchFromLast string
	ch1           chan contracts.Event
	ch2           chan contracts.Event
}

func (p *successfulResumableProvider) Name() string  { return p.name }
func (p *successfulResumableProvider) Priority() int { return contracts.PriorityKV }
func (p *successfulResumableProvider) Load(context.Context) (map[string]any, error) {
	return map[string]any{"server": map[string]any{"port": 7101}}, nil
}
func (p *successfulResumableProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchCalls++
	return p.ch1, nil
}
func (p *successfulResumableProvider) WatchFrom(_ context.Context, last string) (<-chan contracts.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchFromLast = last
	return p.ch2, nil
}

func TestProviderWatch_TriggersReload(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 7777}")},
	}
	p := &fakeWatcherProvider{name: "pw", priority: contracts.PriorityKV, ch: make(chan contracts.Event, 4)}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()

	// After initial load (loadCnt == 1) the port should be 8001.
	gen0 := cfg.Snapshot().Generation
	if got := cfg.Get().Server.Port; got != 8001 {
		t.Fatalf("initial port: got %d want 8001", got)
	}

	// Emit an event — Load() now returns port=8002.
	p.ch <- contracts.Event{Source: "pw", Reason: "tick", At: time.Now()}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cfg.Snapshot().Generation > gen0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cfg.Snapshot().Generation == gen0 {
		t.Fatalf("provider event did not trigger reload (loadCnt=%d)", p.loadCnt.Load())
	}
	if got := cfg.Get().Server.Port; got != 8002 {
		t.Fatalf("after event port: got %d want 8002", got)
	}
}

func TestProviderWatch_PauseSkipsProviderReload(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	p := &fakeWatcherProvider{name: "paused", priority: contracts.PriorityKV, ch: make(chan contracts.Event)}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()

	gen0 := cfg.Snapshot().Generation
	load0 := p.loadCnt.Load()
	cfg.Watcher().Pause()
	if !cfg.Watcher().Paused() {
		t.Fatal("watcher should report paused")
	}

	sendProviderEvent(t, p.ch, contracts.Event{Source: "paused", Reason: "ignored"})
	time.Sleep(150 * time.Millisecond)
	if got := p.loadCnt.Load(); got != load0 {
		t.Fatalf("paused provider event triggered Load: got %d want %d", got, load0)
	}
	if got := cfg.Snapshot().Generation; got != gen0 {
		t.Fatalf("paused provider event advanced generation: got %d want %d", got, gen0)
	}

	cfg.Watcher().Resume()
	sendProviderEvent(t, p.ch, contracts.Event{Source: "paused", Reason: "resumed"})
	waitForProviderWatch(t, 2*time.Second, "provider reload after resume", func() bool {
		return cfg.Snapshot().Generation > gen0
	})
}

func TestProviderWatch_BurstDoesNotBlock(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	p := &fakeWatcherProvider{name: "burst", priority: contracts.PriorityKV, ch: make(chan contracts.Event, 4)}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()
	// Send 100 events in rapid succession; we don't care how many reloads
	// fire — only that the test completes (no deadlock) and at least one
	// reload above gen0 happens.
	gen0 := cfg.Snapshot().Generation
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			select {
			case p.ch <- contracts.Event{Source: "burst", Reason: "spam"}:
			default:
				// drop is fine — channel-side or reloadCh-side overflow
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event producer blocked")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cfg.Snapshot().Generation > gen0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no reload after burst (loadCnt=%d)", p.loadCnt.Load())
}

func TestProviderWatch_ResumableUsesLastRevision(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	p := &successfulResumableProvider{
		name: "resume-ok",
		ch1:  make(chan contracts.Event),
		ch2:  make(chan contracts.Event),
	}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()

	sendProviderEvent(t, p.ch1, contracts.Event{Source: p.name, Reason: "first", Revision: "rev-1"})
	close(p.ch1)

	waitForProviderWatch(t, 2*time.Second, "WatchFrom(last revision)", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.watchCalls == 1 && p.watchFromLast == "rev-1"
	})
}

func TestProviderWatch_EventDroppedWhenReloadQueueFull(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	metrics := &recordingProviderMetrics{}
	p := &blockingWatcherProvider{
		name:              "blocking",
		ch:                make(chan contracts.Event, 128),
		secondLoadStarted: make(chan struct{}),
		releaseSecondLoad: make(chan struct{}),
	}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
		fastconf.WithMetrics(metrics),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		close(p.releaseSecondLoad)
		_ = cfg.Close()
	}()

	p.ch <- contracts.Event{Source: p.name, Reason: "block"}
	select {
	case <-p.secondLoadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second load never started")
	}

	for i := 0; i < 64; i++ {
		p.ch <- contracts.Event{Source: p.name, Reason: "spam"}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if metrics.dropped.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected EventDropped when reload queue filled")
}

func TestProviderWatch_ResumeUnsupportedFallsBackToWatch(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 1}")},
	}
	metrics := &recordingProviderMetrics{}
	p := &fallbackResumableProvider{
		name: "fallback",
		ch1:  make(chan contracts.Event, 1),
		ch2:  make(chan contracts.Event, 1),
	}
	cfg, err := fastconf.New[pwCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
		fastconf.WithWatch(fastconf.WatchOptions{Enabled: true}),
		fastconf.WithMetrics(metrics),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()

	p.ch1 <- contracts.Event{Source: p.name, Reason: "first", Revision: "rev-1"}
	close(p.ch1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		watchCalls := p.watchCalls
		watchFromCalls := p.watchFromCalls
		p.mu.Unlock()
		if watchCalls >= 2 && watchFromCalls >= 1 {
			if metrics.errors.Load() == 0 {
				t.Fatal("resume fallback should record a provider error")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected WatchFrom fallback to plain Watch")
}

func waitForProviderWatch(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func sendProviderEvent(t *testing.T, ch chan contracts.Event, ev contracts.Event) {
	t.Helper()
	select {
	case ch <- ev:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out sending provider event %q", ev.Reason)
	}
}
