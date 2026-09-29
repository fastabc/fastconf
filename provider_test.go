package fastconf_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/cliflag"
	"github.com/fastabc/fastconf/providers/env"
	"github.com/fastabc/fastconf/providers/source"
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

func (f *fakeWatcherProvider) Name() string { return f.name }
func (f *fakeWatcherProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: f.priority}
}
func (f *fakeWatcherProvider) Load(_ context.Context) (contracts.Snapshot, error) {
	n := f.loadCnt.Add(1)
	return contracts.Snapshot{Map: map[string]any{"server": map[string]any{"port": int(8000 + n)}}}, nil
}
func (f *fakeWatcherProvider) Watch(ctx context.Context, _ string) (<-chan contracts.Event, error) {
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
	startOnce         sync.Once
}

func (p *blockingWatcherProvider) Name() string { return p.name }
func (p *blockingWatcherProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}
func (p *blockingWatcherProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	n := p.loadCnt.Add(1)
	if n == 2 {
		p.startOnce.Do(func() { close(p.secondLoadStarted) })
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
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 7777}")},
	}
	p := &fakeWatcherProvider{name: "pw", priority: contracts.PriorityKV, ch: make(chan contracts.Event, 4)}
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

	// After initial load (loadCnt == 1) the port should be 8001.
	gen0 := cfg.Snapshot().Generation()
	if got := cfg.Get().Server.Port; got != 8001 {
		t.Fatalf("initial port: got %d want 8001", got)
	}

	// Emit an event — Load() now returns port=8002.
	p.ch <- contracts.Event{Source: "pw", Reason: "tick", At: time.Now()}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cfg.Snapshot().Generation() > gen0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cfg.Snapshot().Generation() == gen0 {
		t.Fatalf("provider event did not trigger reload (loadCnt=%d)", p.loadCnt.Load())
	}
	if got := cfg.Get().Server.Port; got != 8002 {
		t.Fatalf("after event port: got %d want 8002", got)
	}
}

// TestProviderWatch_RunsWithoutFileWatch verifies that provider Watch is its own
// opt-in, so a provider event reloads even when WithWatch is not set.
func TestProviderWatch_RunsWithoutFileWatch(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {port: 7777}")},
	}
	p := &fakeWatcherProvider{name: "pw", priority: contracts.PriorityKV, ch: make(chan contracts.Event, 4)}
	cfg, err := fastconf.New[pwCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithProvider(p))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	p.ch <- contracts.Event{Source: "pw", Reason: "tick", At: time.Now()}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && cfg.Get().Server.Port != 8002 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := cfg.Get().Server.Port; got != 8002 {
		t.Fatalf("provider event without WithWatch: port %d; want 8002", got)
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
	waitForProviderWatch(t, 2*time.Second, "provider reload after resume", func() bool {
		return cfg.Snapshot().Generation() > gen0
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
		fastconf.WithWatch(fastconf.Watch{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	// Send 100 events in rapid succession; we don't care how many reloads
	// fire — only that the test completes (no deadlock) and at least one
	// reload above gen0 happens.
	gen0 := cfg.Snapshot().Generation()
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
		if cfg.Snapshot().Generation() > gen0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no reload after burst (loadCnt=%d)", p.loadCnt.Load())
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
		fastconf.WithWatch(fastconf.Watch{}),
		fastconf.WithObserver(metrics),
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

// Provider map ownership: contracts.Provider.Load says the provider
// remains the owner of the returned map. These tests pin the assembly
// boundary clone that keeps pipeline stages (typed hooks, deep merge,
// secret resolve) from mutating provider-owned — or user-owned — maps.

type ownershipCfg struct {
	Name string `json:"name"`
	// RPC must stay absent from the newFS base layers: the aliasing
	// under test only happens when the provider is the first layer to
	// introduce the subtree.
	RPC struct {
		Timeout time.Duration `json:"timeout"`
	} `json:"rpc"`
	DB struct {
		Host string `json:"host"`
	} `json:"db"`
}

// cachedMapProvider models providers (like cliflag.CLIProvider) that
// return the same long-lived map from every Load call.
type cachedMapProvider struct {
	name string
	prio int
	data map[string]any
}

func (p *cachedMapProvider) Name() string { return p.name }
func (p *cachedMapProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.prio}
}
func (p *cachedMapProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data}, nil
}
func (p *cachedMapProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

func TestProviderMapNotMutatedByTypedHooks(t *testing.T) {
	orig := map[string]any{"rpc": map[string]any{"timeout": "30s"}}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(cliflag.NewCLI(orig)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().RPC.Timeout; got != 30*time.Second {
		t.Fatalf("timeout = %v", got)
	}
	// The duration hook rewrites "30s" → int64 in the merged tree; the
	// caller's map (aliased by cliflag) must keep the original string.
	if v, ok := orig["rpc"].(map[string]any)["timeout"].(string); !ok || v != "30s" {
		t.Errorf("provider map mutated by pipeline: %T(%v)",
			orig["rpc"].(map[string]any)["timeout"],
			orig["rpc"].(map[string]any)["timeout"])
	}
}

func TestProviderMapsDoNotCrossContaminate(t *testing.T) {
	lowMap := map[string]any{"db": map[string]any{"host": "low"}}
	highMap := map[string]any{"db": map[string]any{"host": "high"}}
	low := &cachedMapProvider{name: "low", prio: 100, data: lowMap}
	high := &cachedMapProvider{name: "high", prio: 200, data: highMap}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(low),
		fastconf.WithProvider(high),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().DB.Host; got != "high" {
		t.Fatalf("merge order: host = %q", got)
	}
	// Merging high's layer over low's aliased subtree must not write
	// high's value into low's own map.
	if v := lowMap["db"].(map[string]any)["host"]; v != "low" {
		t.Errorf("low provider map contaminated by higher layer: %v", v)
	}
}

// A provider's snapshot revision reaches ReloadCause unchanged.

// snapshotProvider returns a revisioned snapshot.
type snapshotProvider struct {
	name string
	prio int
	rev  string
	data map[string]any
}

func (p *snapshotProvider) Name() string { return p.name }
func (p *snapshotProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.prio}
}
func (p *snapshotProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data, Revision: p.rev}, nil
}
func (p *snapshotProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

func TestProviderRevisionReachesCause(t *testing.T) {
	p := &snapshotProvider{
		name: "etcdish",
		rev:  "rev-42",
		data: map[string]any{"db": map[string]any{"host": "ordered"}},
	}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	revs := mgr.Snapshot().Cause().Revisions
	if got := revs[p.name]; got != p.rev {
		t.Fatalf("revision lost: got %q, want %q (revisions=%v)",
			got, p.rev, revs)
	}
}

// TestNew_MissingBaseDirIsZeroFileLayers verifies that a provider-only manager
// must not need an on-disk conf.d/base.
func TestNew_MissingBaseDirIsZeroFileLayers(t *testing.T) {
	type cfg struct {
		Port int `json:"port"`
	}
	src := source.NewBytes("inline", "yaml", []byte("port: 7\n"))
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(src),
	)
	if err != nil {
		t.Fatalf("New without conf.d/base: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Port; got != 7 {
		t.Fatalf("port = %d; want 7", got)
	}

	if _, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fstest.MapFS{})); !errors.Is(err, fastconf.ErrNoSources) {
		t.Fatalf("no sources at all: err = %v; want ErrNoSources", err)
	}
	if _, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(src), fastconf.WithStrictMerge(true),
	); err == nil {
		t.Fatal("strict mode must still require the base directory")
	}
}

// TestSourceDefaultsRankBelowEnvAndCLI verifies that source.* defaults sit in the
// user priority range, so env and CLI keep overriding sourced documents.
func TestSourceDefaultsRankBelowEnvAndCLI(t *testing.T) {
	type cfg struct {
		Port int    `json:"port"`
		Host string `json:"host"`
	}
	path := filepath.Join(t.TempDir(), "extra.json")
	if err := os.WriteFile(path, []byte(`{"port":1,"host":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("F6TEST_PORT", "2")
	t.Setenv("F6TEST_HOST", "env")
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithProvider(source.NewBytes("inline", "json", []byte(`{"port":1,"host":"bytes"}`))),
		fastconf.WithProvider(env.NewEnv("F6TEST_")),
		fastconf.WithProvider(cliflag.NewCLI(map[string]any{"port": 3})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get(); got.Port != 3 || got.Host != "env" {
		t.Fatalf("got port=%d host=%q; want CLI port 3 and env host", got.Port, got.Host)
	}
}

func TestBytesProvider_OverridesFileLayers(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(source.NewBytes("override", "yaml", []byte("database:\n  dsn: from-bytes\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if mgr.Get().Database.DSN != "from-bytes" {
		t.Errorf("bytes source did not win: %q", mgr.Get().Database.DSN)
	}
	// pool still comes from base
	if mgr.Get().Database.Pool != 10 {
		t.Errorf("pool lost: %d", mgr.Get().Database.Pool)
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

type portCfg struct {
	Port int `json:"port"`
}

// TestProvider_DescribePriorityOrdersLayers verifies: priority is
// provider metadata read through Describe, not a method on Provider.
func TestProvider_DescribePriorityOrdersLayers(t *testing.T) {
	low := &contractProvider{name: "low", prio: contracts.PriorityKV, port: 1}
	high := &contractProvider{name: "high", prio: contracts.PriorityEnv, port: 2}
	mgr, err := fastconf.New[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(high), fastconf.WithProvider(low))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Port; got != 2 {
		t.Fatalf("port = %d; want 2 from the Describe()-priority-50 provider", got)
	}
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
