package fastconf_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/observe"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

type recObserver struct {
	mu     sync.Mutex
	events []fastconf.Event
}

func (r *recObserver) Observe(_ context.Context, e fastconf.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recObserver) snapshot() []fastconf.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fastconf.Event(nil), r.events...)
}

// TestObserver_ReceivesReloadLifecycle verifies: one Observer sees
// reload start/finish, stage timings and the committed change with a lazy
// diff.
func TestObserver_ReceivesReloadLifecycle(t *testing.T) {
	rec := &recObserver{}
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"),
		fastconf.WithObserver(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"server": map[string]any{"addr": ":1"}})); err != nil {
		t.Fatal(err)
	}
	var started, finished, stages int
	var committed *fastconf.Committed
	for _, e := range rec.snapshot() {
		switch ev := e.(type) {
		case fastconf.ReloadStarted:
			started++
		case fastconf.ReloadFinished:
			if ev.Err != nil {
				t.Fatalf("unexpected reload error: %v", ev.Err)
			}
			finished++
		case fastconf.StageFinished:
			stages++
		case fastconf.Committed:
			c := ev
			committed = &c
		}
	}
	if started != 2 || finished != 2 || stages == 0 {
		t.Fatalf("started=%d finished=%d stages=%d; want 2, 2, >0", started, finished, stages)
	}
	if committed == nil || committed.Next != mgr.Snapshot().Generation() || committed.Cause.Reason != "override" {
		t.Fatalf("committed = %+v", committed)
	}
	diff := committed.Diff()
	if len(diff) != 1 || diff[0].Path != "server.addr" {
		t.Fatalf("Committed.Diff() = %+v; want the server.addr change", diff)
	}
}

// TestObserve_AsyncMultiJSONLines verifies the helper package.
func TestObserve_AsyncMultiJSONLines(t *testing.T) {
	var buf safeBuffer
	rec := &recObserver{}
	async := observe.Async(rec, 16)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"),
		fastconf.WithObserver(observe.Multi(async, observe.JSONLines(&buf))))
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"server": map[string]any{"addr": ":2"}})); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := async.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rec.snapshot()) == 0 {
		t.Fatal("async observer received nothing")
	}
	if !strings.Contains(buf.String(), `"reason":"override"`) {
		t.Fatalf("JSONLines output missing the committed cause: %q", buf.String())
	}
}

// TestObserver_TimeoutBoundsSlowObserver pins WithObserverTimeout: a slow
// observer's ctx is canceled at the deadline.
func TestObserver_TimeoutBoundsSlowObserver(t *testing.T) {
	var sawDeadline bool
	slow := observe.Func(func(ctx context.Context, e fastconf.Event) {
		if _, ok := e.(fastconf.Committed); !ok {
			return
		}
		<-ctx.Done()
		sawDeadline = true
	})
	start := time.Now()
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"),
		fastconf.WithObserver(slow), fastconf.WithObserverTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if !sawDeadline || time.Since(start) > 2*time.Second {
		t.Fatalf("observer ctx not bounded: sawDeadline=%v elapsed=%v", sawDeadline, time.Since(start))
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type obsCfg struct {
	Name string `json:"name"`
}

func observerManager(t *testing.T, opts ...fastconf.Option) *fastconf.Manager[obsCfg] {
	t.Helper()
	opts = append(opts, fastconf.WithFS(fstest.MapFS{"conf.d/base/a.yaml": &fstest.MapFile{Data: []byte("name: initial\n")}}))
	m, err := fastconf.New[obsCfg](context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("observer timed out")
		var zero T
		return zero
	}
}

// onCommit returns an observer that runs fn for Committed events after the
// initial load.
func onCommit(fn func(ctx context.Context, c fastconf.Committed)) fastconf.Observer {
	return observe.Func(func(ctx context.Context, e fastconf.Event) {
		if c, ok := e.(fastconf.Committed); ok && c.Cause.Reason != "initial" {
			fn(ctx, c)
		}
	})
}

func TestObserver_NoCommittedWhenNoChange(t *testing.T) {
	var commits atomic.Int32
	m := observerManager(t, fastconf.WithObserver(onCommit(func(context.Context, fastconf.Committed) { commits.Add(1) })))
	// Identical reload: hash dedupe skips the swap entirely.
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := commits.Load(); got != 0 {
		t.Errorf("Committed on idempotent reload: %d", got)
	}
}

func TestObserverTimeoutOption(t *testing.T) {
	for _, d := range []time.Duration{0, -1, time.Minute} {
		t.Run(d.String(), func(t *testing.T) {
			remaining := make(chan time.Duration, 1)
			m := observerManager(t, fastconf.WithObserverTimeout(d), fastconf.WithObserver(onCommit(func(ctx context.Context, _ fastconf.Committed) {
				deadline, ok := ctx.Deadline()
				if !ok {
					remaining <- -1
					return
				}
				remaining <- time.Until(deadline)
			})))
			if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "next"})); err != nil {
				t.Fatal(err)
			}
			got, want := await(t, remaining), d
			if d == 0 {
				want = fastconf.DefaultObserverTimeout
			}
			if want < 0 {
				if got != -1 {
					t.Errorf("disabled timeout has deadline: %v", got)
				}
			} else if got > want || got < want/2 {
				t.Errorf("deadline remaining=%v want near %v", got, want)
			}
		})
	}
}

// TestObserverTimeoutPreservesOrderAndUnblocksReload: a slow observer is cut
// off at the deadline, the next observer still runs, and the reload
// publishes.
func TestObserverTimeoutPreservesOrderAndUnblocksReload(t *testing.T) {
	var order []string
	m := observerManager(t, fastconf.WithObserverTimeout(20*time.Millisecond),
		fastconf.WithObserver(
			onCommit(func(ctx context.Context, _ fastconf.Committed) { order = append(order, "first"); <-ctx.Done() }),
			onCommit(func(context.Context, fastconf.Committed) { order = append(order, "second") }),
		))
	for _, name := range []string{"one", "two"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := m.Reload(ctx, fastconf.WithOverride(map[string]any{"name": name}))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if m.Get().Name != name {
			t.Fatal("observer timeout lost publication")
		}
	}
	if strings.Join(order, ",") != "first,second,first,second" {
		t.Fatalf("order=%v", order)
	}
}

func TestCloseCancelsInFlightObserver(t *testing.T) {
	entered, canceled := make(chan struct{}, 1), make(chan error, 1)
	m := observerManager(t, fastconf.WithObserverTimeout(-1), fastconf.WithObserver(onCommit(func(ctx context.Context, _ fastconf.Committed) {
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- ctx.Err()
	})))
	reload := make(chan error, 1)
	go func() {
		reload <- m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "next"}))
	}()
	await(t, entered)
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	if err := await(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := await(t, canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("observer ctx=%v", err)
	}
	_ = await(t, reload)
}

func TestObserverIgnoringContextKeepsShutdownPending(t *testing.T) {
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	m := observerManager(t, fastconf.WithObserver(onCommit(func(context.Context, fastconf.Committed) { entered <- struct{}{}; <-gate })))
	// Always release before observerManager's cleanup, including on failure.
	defer close(gate)
	go func() { _ = m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "next"})) }()
	await(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown=%v", err)
	}
	select {
	case _, ok := <-m.Errors():
		if !ok {
			t.Fatal("Errors closed while the reload goroutine still runs")
		}
	default:
	}
}

// TestObserveAsync_DropsWhenQueueFull: Async never blocks the reload
// goroutine; overflow is counted instead of queued without bound.
func TestObserveAsync_DropsWhenQueueFull(t *testing.T) {
	gate := make(chan struct{})
	async := observe.Async(onCommit(func(context.Context, fastconf.Committed) { <-gate }), 2)
	m := observerManager(t, fastconf.WithObserver(async))
	for i := 0; i < 10; i++ {
		if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": fmt.Sprint("v", i)})); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
	}
	if async.Dropped() == 0 {
		t.Fatal("expected dropped events once the queue filled")
	}
	close(gate)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := async.Close(); err != nil {
		t.Fatal(err)
	}
}

// promLike has the method set of observability/metrics/prometheus.Sink.
type promLike struct {
	mu       sync.Mutex
	ok, errs int
	gen      uint64
	layers   int
	stages   map[string]bool
}

func (p *promLike) ReloadStarted() {}
func (p *promLike) ReloadFinished(ok bool, _ time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ok {
		p.ok++
	} else {
		p.errs++
	}
}
func (p *promLike) StateGeneration(g uint64) { p.mu.Lock(); p.gen = g; p.mu.Unlock() }
func (p *promLike) LayersTotal(n int)        { p.mu.Lock(); p.layers = n; p.mu.Unlock() }
func (p *promLike) ProviderError(string)     {}
func (p *promLike) EventDropped(string)      {}
func (p *promLike) StageDuration(stage string, _ time.Duration, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages[stage] = ok
}

// TestObserveMetrics_AdaptsSinkMethodSet: the prometheus satellite's Sink
// plugs into WithObserver through observe.Metrics without importing root.
func TestObserveMetrics_AdaptsSinkMethodSet(t *testing.T) {
	sink := &promLike{stages: map[string]bool{}}
	m := observerManager(t, fastconf.WithObserver(observe.Metrics(sink)))
	if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "next"})); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.ok != 2 || sink.errs != 0 || sink.gen != m.Snapshot().Generation() || sink.layers == 0 || !sink.stages["decode"] {
		t.Fatalf("sink = %+v", sink)
	}
}

type cfgAudit struct {
	Name string `yaml:"name"`
}

func TestObserver_CommittedCarriesInitialCause(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: hello\n")},
	}
	var mu sync.Mutex
	var seen []fastconf.ReloadCause
	rec := observe.Func(func(_ context.Context, e fastconf.Event) {
		if c, ok := e.(fastconf.Committed); ok {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, c.Cause)
		}
	})
	mgr, err := fastconf.New[cfgAudit](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithObserver(rec),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	mu.Lock()
	n := len(seen)
	if n != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 Committed event, got %d", n)
	}
	first := seen[0]
	mu.Unlock()
	if first.Reason != "initial" || first.At == 0 {
		t.Fatalf("bad cause %+v", first)
	}
	if got := mgr.Snapshot().Cause(); got.Reason != "initial" {
		t.Fatalf("State.Cause() not surfaced: %+v", got)
	}
}

func TestObserveJSONLines_OneLinePerCommit(t *testing.T) {
	var buf bytes.Buffer
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	mgr, err := fastconf.New[cfgAudit](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithObserver(observe.JSONLines(&buf)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"name": "y"})); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 2 {
		t.Fatalf("expected 2 lines, got %d (%q)", lines, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"reason":"initial"`)) || !bytes.Contains(buf.Bytes(), []byte(`"generation":2`)) {
		t.Fatalf("JSONLines output missing fields: %s", buf.String())
	}
}

func TestPipeline_StageDebugLogs(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	logs := buf.String()
	if strings.Contains(logs, `"msg":"fastconf: stage"`) {
		t.Fatalf("unexpected pre-run stage log in logs:\n%s", logs)
	}
	if got := strings.Count(logs, `"msg":"stage done"`); got != 8 {
		t.Fatalf("stage log count = %d, want 8; logs:\n%s", got, logs)
	}
	for _, want := range []string{"merge", "transform", "secret", "typed-hooks", "decode", "field-meta", "validate", "policy"} {
		if !strings.Contains(logs, `"stage":"`+want+`"`) {
			t.Fatalf("missing stage %q in logs:\n%s", want, logs)
		}
	}
	if !strings.Contains(logs, `"elapsed"`) {
		t.Fatalf("missing elapsed field in logs:\n%s", logs)
	}
}

func TestPipeline_StageErrorLogsFailure(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	_, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
		fastconf.WithValidate(func(*appCfg) error { return errors.New("boom") }),
	)
	if err == nil {
		t.Fatal("expected validator failure")
	}

	logs := buf.String()
	if !strings.Contains(logs, `"msg":"stage error"`) {
		t.Fatalf("missing stage error log:\n%s", logs)
	}
	if !strings.Contains(logs, `"stage":"validate"`) {
		t.Fatalf("missing validate stage in logs:\n%s", logs)
	}
}

func TestReload_LogMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"swap", "name: updated\n", "fastconf reload swap", false},
		{"assemble failure", "name: [\n", "fastconf reload shadow_failed", true},
		{"commit failure", "name: invalid\n", "fastconf reload commit_failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("name: initial\n")}}
			var buf bytes.Buffer
			mgr, err := fastconf.New[cfgAudit](context.Background(),
				fastconf.WithFS(fs),
				fastconf.WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
				fastconf.WithValidate(func(c *cfgAudit) error {
					if c.Name == "invalid" {
						return errors.New("invalid name")
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = mgr.Close() }()
			buf.Reset()
			fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte(tc.content)}
			if err := mgr.Reload(context.Background()); (err != nil) != tc.wantErr {
				t.Fatalf("Reload error = %v, want error = %t", err, tc.wantErr)
			}
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				var record struct {
					Message string `json:"msg"`
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				if record.Message == tc.want {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("log message %q occurred %d times, want 1; logs:\n%s", tc.want, count, buf.String())
			}
		})
	}
}

// recordingMetrics counts the events a metrics exporter consumes.
type recordingMetrics struct {
	started  atomic.Int64
	okCount  atomic.Int64
	errCount atomic.Int64
	gen      atomic.Uint64
	layers   atomic.Int64
}

func (r *recordingMetrics) Observe(_ context.Context, e fastconf.Event) {
	switch ev := e.(type) {
	case fastconf.ReloadStarted:
		r.started.Add(1)
	case fastconf.ReloadFinished:
		if ev.Err == nil {
			r.okCount.Add(1)
		} else {
			r.errCount.Add(1)
		}
	case fastconf.Committed:
		r.gen.Store(ev.Next)
		r.layers.Store(int64(ev.Layers))
	}
}

func TestObservability_SlogAndMetrics(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	rec := &recordingMetrics{}

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
		fastconf.WithObserver(rec),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if rec.started.Load() != 1 || rec.okCount.Load() != 1 {
		t.Errorf("metrics counters: started=%d ok=%d", rec.started.Load(), rec.okCount.Load())
	}
	if rec.gen.Load() != 1 || rec.layers.Load() != 2 {
		t.Errorf("gauges: gen=%d layers=%d", rec.gen.Load(), rec.layers.Load())
	}

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"initial"`) || !strings.Contains(logs, "fastconf reload swap") {
		t.Errorf("expected reload log entries, got:\n%s", logs)
	}
}

// TestOptions_NilLoggerAndTracerFailConstruction verifies that passing nil to
// WithLogger / WithObserver / WithTracer surfaces as a DeferredErr from New(),
// not a silent drop.
func TestOptions_NilLoggerAndTracerFailConstruction(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	type cfg struct {
		Name string `json:"name"`
	}

	cases := []struct {
		name string
		opt  fastconf.Option
		want string
	}{
		{"WithLogger", fastconf.WithLogger(nil), "WithLogger(nil)"},
		{"WithObserver", fastconf.WithObserver(nil), "WithObserver: nil observer"},
		{"WithTracer", fastconf.WithTracer(nil), "WithTracer(nil)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fastconf.New[cfg](context.Background(),
				fastconf.WithFS(mfs),
				fastconf.WithDir("conf.d"),
				tc.opt,
			)
			if err == nil {
				t.Fatal("expected nil-option to surface as deferred error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestObserver_OverrideUsesReloadLifecycle verifies: an override
// reload is an ordinary reload with one more layer, so it reports the same
// metrics and sorts its layer with every other source.
func TestObserver_OverrideUsesReloadLifecycle(t *testing.T) {
	rec := &recordingMetrics{}
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithObserver(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	started, ok := rec.started.Load(), rec.okCount.Load()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"server": map[string]any{"addr": ":1"}})); err != nil {
		t.Fatal(err)
	}
	if rec.started.Load() != started+1 || rec.okCount.Load() != ok+1 {
		t.Fatalf("override reload metrics: started %d->%d ok %d->%d; want +1 each",
			started, rec.started.Load(), ok, rec.okCount.Load())
	}
	srcs := mgr.Snapshot().Sources()
	if last := srcs[len(srcs)-1]; last.Kind != fastconf.LayerOverride {
		t.Fatalf("override must be the highest-ranked layer, got %+v", last)
	}
}
