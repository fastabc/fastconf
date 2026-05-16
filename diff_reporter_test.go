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
)

type drCfg struct {
	Name string `json:"name"`
}

type recordingReporter struct {
	mu     sync.Mutex
	events []fastconf.DiffEvent
}

func (r *recordingReporter) Report(_ context.Context, ev fastconf.DiffEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recordingReporter) snapshot() []fastconf.DiffEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]fastconf.DiffEvent, len(r.events))
	copy(cp, r.events)
	return cp
}

func TestDiffReporter_FiresAfterCommit(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: alpha\n")},
	}
	r := &recordingReporter{}
	mgr, err := fastconf.New[drCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithDiffReporter(r),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Inject a one-shot override that actually changes the value, so
	// State.Diff produces a non-empty list.
	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"name": "beta",
	})); err != nil {
		t.Fatal(err)
	}

	// Reporter is async; wait briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.snapshot()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	events := r.snapshot()
	if len(events) == 0 {
		t.Fatal("no diff event received")
	}
	if events[0].Reason == "" {
		t.Errorf("expected non-empty reason: %+v", events[0])
	}
	if len(events[0].Diff) == 0 {
		t.Errorf("expected non-empty diff slice: %+v", events[0])
	}
}

func TestDiffReporter_AsyncFailureSwallowedAndLogged(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: alpha\n")},
	}
	var calls atomic.Int32
	r := fastconf.DiffReporterFunc(func(_ context.Context, _ fastconf.DiffEvent) error {
		calls.Add(1)
		return errors.New("downstream broke")
	})
	mgr, err := fastconf.New[drCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithDiffReporter(r),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"name": "gamma",
	})); err != nil {
		t.Fatal(err)
	}
	// Manager.Reload should still report success even if reporter fails.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Error("reporter was never invoked")
	}
}

// blockingReporter holds Report() open on a gate so we can fill the
// per-reporter queue and exercise the drop-on-full backpressure path.
type blockingReporter struct {
	gate     chan struct{}
	enqueued atomic.Int32 // increments when Report unblocks
}

func (r *blockingReporter) Report(_ context.Context, _ fastconf.DiffEvent) error {
	r.enqueued.Add(1)
	<-r.gate
	return nil
}

// countingMetrics implements MetricsSink + ProviderMetricsSink + the new
// DiffReporterMetricsSink so we can assert backpressure fires and the
// queue-depth gauge is fed.
type countingMetrics struct {
	dropped atomic.Int32

	depthMu  sync.Mutex
	depthMax map[string]int // reporter label → max observed depth
	depthCap map[string]int // reporter label → capacity reported
}

func (m *countingMetrics) ReloadStarted()                                  {}
func (m *countingMetrics) ReloadFinished(_ bool, _ time.Duration)          {}
func (m *countingMetrics) StateGeneration(_ uint64)                        {}
func (m *countingMetrics) LayersTotal(_ int)                               {}
func (m *countingMetrics) ProviderError(_ string)                          {}
func (m *countingMetrics) EventDropped(_ string)                           { m.dropped.Add(1) }
func (m *countingMetrics) StageDuration(_ string, _ time.Duration, _ bool) {}
func (m *countingMetrics) DiffReporterQueueDepth(reporter string, depth, capacity int) {
	m.depthMu.Lock()
	defer m.depthMu.Unlock()
	if m.depthMax == nil {
		m.depthMax = map[string]int{}
		m.depthCap = map[string]int{}
	}
	if depth > m.depthMax[reporter] {
		m.depthMax[reporter] = depth
	}
	m.depthCap[reporter] = capacity
}
func (m *countingMetrics) maxDepth(label string) (depth, capacity int) {
	m.depthMu.Lock()
	defer m.depthMu.Unlock()
	return m.depthMax[label], m.depthCap[label]
}

// TestDiffReporter_BoundedQueueDropsOnOverflow asserts P1.2: a slow
// reporter does not lead to unbounded goroutine fan-out. Once the
// per-reporter queue (cap=2 here) fills, further events are dropped and
// EventDropped("diff-reporter") fires on the MetricsSink.
func TestDiffReporter_BoundedQueueDropsOnOverflow(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: alpha\n")},
	}
	r := &blockingReporter{gate: make(chan struct{})}
	mx := &countingMetrics{}
	mgr, err := fastconf.New[drCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithDiffReporter(r),
		fastconf.WithDiffReporterQueueCap(2),
		fastconf.WithMetrics(mx),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(r.gate) // release reporter so workers can exit
		mgr.Close()
	})

	// 10 distinct reloads → 1 in flight, 2 queued, 7 dropped.
	for i := 0; i < 10; i++ {
		if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
			"name": time.Now().Format(time.RFC3339Nano) + "-" + string(rune('a'+i)),
		})); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
	}

	// Allow the worker to pull the in-flight event and the next two queue
	// slots to settle.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mx.dropped.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := mx.dropped.Load(); got == 0 {
		t.Fatalf("expected at least one dropped diff event, got 0 (queue not saturating)")
	}
}

// TestDiffReporter_QueueDepthSampled verifies T2: every fireDiffReporters
// call also pushes (depth, capacity) through DiffReporterMetricsSink so
// operators can build a Prometheus gauge that tracks how close each
// reporter is to its drop-on-full threshold.
func TestDiffReporter_QueueDepthSampled(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: alpha\n")},
	}
	r := &blockingReporter{gate: make(chan struct{})}
	mx := &countingMetrics{}
	mgr, err := fastconf.New[drCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithDiffReporter(r),
		fastconf.WithDiffReporterQueueCap(4),
		fastconf.WithMetrics(mx),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(r.gate)
		mgr.Close()
	})

	// 5 reloads → 1 in-flight + 4 queued → next event should be at depth=4
	// before being dropped. The first sample comes from the very first
	// fire, so we accept "at least one sample seen" as the green path.
	for i := 0; i < 5; i++ {
		if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
			"name": "v" + string(rune('a'+i)),
		})); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
	}
	// Allow at least one sample to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d, _ := mx.maxDepth("diff-reporter:0"); d > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	depth, capacity := mx.maxDepth("diff-reporter:0")
	if capacity != 4 {
		t.Errorf("queue-depth sample capacity: want 4, got %d", capacity)
	}
	if depth < 1 || depth > 4 {
		t.Errorf("queue-depth sample peak: want 1..4, got %d", depth)
	}
}

func TestDiffReporter_NoFireWhenNoChange(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: alpha\n")},
	}
	r := &recordingReporter{}
	mgr, err := fastconf.New[drCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithDiffReporter(r),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	// Identical reload — hash dedupe should skip the swap entirely.
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(r.snapshot()); got != 0 {
		t.Errorf("expected 0 events on idempotent reload, got %d", got)
	}
}
