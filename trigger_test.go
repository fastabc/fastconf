package fastconf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

// causeRecorder captures the cause of every Committed event.
type causeRecorder chan ReloadCause

func (c causeRecorder) Observe(_ context.Context, e Event) {
	if ev, ok := e.(Committed); ok {
		select {
		case c <- ev.Cause:
		default:
		}
	}
}

// awaitTrigger waits for a commit whose Reason satisfies match and checks its Trigger.
func awaitTrigger(t *testing.T, causes causeRecorder, want ReloadTrigger, match func(string) bool) ReloadCause {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c := <-causes:
			if !match(c.Reason) {
				continue
			}
			if c.Trigger != want {
				t.Fatalf("commit reason %q: trigger %q; want %q", c.Reason, c.Trigger, want)
			}
			return c
		case <-timeout:
			t.Fatalf("no commit with trigger %q", want)
			return ReloadCause{}
		}
	}
}

func reasonIs(want string) func(string) bool {
	return func(r string) bool { return r == want }
}

func reasonPrefix(prefix string) func(string) bool {
	return func(r string) bool { return strings.HasPrefix(r, prefix) }
}

// changingProvider returns a new value on every Load so each reload commits.
type changingProvider struct {
	n     atomic.Int64
	watch chan contracts.Event
}

func (*changingProvider) Name() string { return "changing" }

func (p *changingProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"n": p.n.Add(1)}}, nil
}

type changingWatchProvider struct{ *changingProvider }

func (p changingWatchProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return p.watch, nil
}

// onceNotifier requests one refresh, then waits for cancellation.
type onceNotifier struct{}

func (onceNotifier) Run(ctx context.Context, refresh func(context.Context) error) error {
	_ = refresh(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func TestReloadTriggerSynchronousPaths(t *testing.T) {
	causes := make(causeRecorder, 32)
	m, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`))),
		WithHistory(4), WithObserver(causes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	awaitTrigger(t, causes, TriggerInitial, reasonIs("initial"))
	if got := m.Snapshot().Cause().Trigger; got != TriggerInitial {
		t.Fatalf("initial snapshot trigger = %q", got)
	}
	target := m.Snapshot()

	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	awaitTrigger(t, causes, TriggerManual, reasonIs("override"))
	if err := m.Reload(context.Background(), WithReason("provider:spoofed"), WithOverride(map[string]any{"name": "gamma"})); err != nil {
		t.Fatal(err)
	}
	// A caller-chosen reason that looks like another source keeps its trigger.
	awaitTrigger(t, causes, TriggerManual, reasonIs("provider:spoofed"))

	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Proposed.Cause().Trigger; got != TriggerPlan {
		t.Fatalf("plan trigger = %q; want %q", got, TriggerPlan)
	}

	if err := m.History().Rollback(target); err != nil {
		t.Fatal(err)
	}
	awaitTrigger(t, causes, TriggerRollback, reasonIs("rollback"))
	if got := m.Snapshot().Cause().Trigger; got != TriggerRollback {
		t.Fatalf("rolled-back snapshot trigger = %q", got)
	}

	state, err := Load[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`))))
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Cause().Trigger; got != TriggerInitial {
		t.Fatalf("Load trigger = %q; want %q", got, TriggerInitial)
	}
}

func TestReloadTriggerBackgroundPaths(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		causes := make(causeRecorder, 32)
		p := changingWatchProvider{&changingProvider{watch: make(chan contracts.Event, 1)}}
		m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p), WithObserver(causes))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		p.watch <- contracts.Event{Reason: "tick"}
		awaitTrigger(t, causes, TriggerProvider, reasonIs("provider:changing:tick"))
	})
	t.Run("provider overflow resync", func(t *testing.T) {
		causes := make(causeRecorder, 32)
		p := &resyncProvider{name: "value"}
		p.value.Store(1)
		m, err := New[map[string]int](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p), WithObserver(causes))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		release := fillResyncQueue(t, m)
		p.value.Store(2)
		sendResyncEvent(m, p)
		release()
		awaitTrigger(t, causes, TriggerProvider, reasonIs("provider:resync"))
	})
	t.Run("notify", func(t *testing.T) {
		causes := make(causeRecorder, 32)
		m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{}),
			WithProvider(&changingProvider{}), WithNotifier(onceNotifier{}), WithObserver(causes))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		awaitTrigger(t, causes, TriggerNotify, reasonPrefix("notify"))
	})
	t.Run("resync", func(t *testing.T) {
		causes := make(causeRecorder, 32)
		m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{}),
			WithProvider(&changingProvider{}), WithResync(10*time.Millisecond), WithObserver(causes))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		awaitTrigger(t, causes, TriggerResync, reasonIs("resync"))
	})
	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "base", "00.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("name: a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		causes := make(causeRecorder, 32)
		m, err := New[snapshotConfig](context.Background(), WithDir(dir),
			WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}), WithObserver(causes))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		if err := os.WriteFile(path, []byte("name: b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c := awaitTrigger(t, causes, TriggerFile, reasonPrefix("fs:"))
		if c.Key == "" {
			t.Fatalf("file commit has no Key: %+v", c)
		}
	})
}

func TestReloadTriggerOnErrors(t *testing.T) {
	rejected := errors.New("rejected")
	var fail atomic.Bool
	m, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`))),
		WithValidate(func(*snapshotConfig) error {
			if fail.Load() {
				return rejected
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	next := func(op string) ReloadError {
		t.Helper()
		select {
		case re := <-m.Errors():
			return re
		case <-time.After(3 * time.Second):
			t.Fatalf("%s failure missing from Errors", op)
			return ReloadError{}
		}
	}

	fail.Store(true)
	if err := m.Reload(context.Background()); !errors.Is(err, rejected) {
		t.Fatalf("Reload = %v; want %v", err, rejected)
	}
	if re := next("reload"); re.Trigger != TriggerManual || re.Reason != "manual" {
		t.Fatalf("reload error = reason %q trigger %q; want manual/manual", re.Reason, re.Trigger)
	}
	fail.Store(false)

	// A subscriber panic is reported under the trigger of the commit it observed.
	Subscribe(m, func(c *snapshotConfig) *string { return &c.Name }, func(_, _ *string) { panic("boom") })
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	if re := next("subscriber"); re.Trigger != TriggerManual || re.Reason != "subscriber-panic" {
		t.Fatalf("subscriber error = reason %q trigger %q; want subscriber-panic/manual", re.Reason, re.Trigger)
	}
}

// eventTriggers records the Trigger of every reload and stage event.
type eventTriggers struct {
	mu   sync.Mutex
	seen map[string][]ReloadTrigger
}

func (r *eventTriggers) Observe(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev := e.(type) {
	case ReloadStarted:
		r.seen["started"] = append(r.seen["started"], ev.Trigger)
	case ReloadFinished:
		r.seen["finished"] = append(r.seen["finished"], ev.Trigger)
	case StageFinished:
		r.seen["stage"] = append(r.seen["stage"], ev.Trigger)
	}
}

// take returns and clears the recorded triggers.
func (r *eventTriggers) take() map[string][]ReloadTrigger {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.seen
	r.seen = map[string][]ReloadTrigger{}
	return out
}

func assertEventTriggers(t *testing.T, got map[string][]ReloadTrigger, want ReloadTrigger, kinds ...string) {
	t.Helper()
	for _, kind := range kinds {
		if len(got[kind]) == 0 {
			t.Errorf("no %s events", kind)
		}
		for _, tr := range got[kind] {
			if tr != want {
				t.Errorf("%s trigger = %q; want %q", kind, tr, want)
			}
		}
	}
}

func TestReloadEventsCarryTrigger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base", "00.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &eventTriggers{seen: map[string][]ReloadTrigger{}}
	causes := make(causeRecorder, 32)
	m, err := New[snapshotConfig](context.Background(), WithDir(dir),
		WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}),
		WithObserver(rec, causes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	assertEventTriggers(t, rec.take(), TriggerInitial, "started", "finished", "stage")
	awaitTrigger(t, causes, TriggerInitial, reasonIs("initial"))

	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertEventTriggers(t, rec.take(), TriggerManual, "started", "finished", "stage")

	if _, err := m.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.take()
	assertEventTriggers(t, got, TriggerPlan, "stage")
	if len(got["started"])+len(got["finished"]) != 0 {
		t.Errorf("Plan emitted reload events: %v", got)
	}

	if err := os.WriteFile(path, []byte("name: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	awaitTrigger(t, causes, TriggerFile, reasonPrefix("fs:"))
	// Committed is emitted inside the commit stage, before its StageFinished
	// and ReloadFinished; drain the writer so the file reload has finished.
	drainWriter(t, m)
	assertEventTriggers(t, rec.take(), TriggerFile, "started", "finished", "stage")
}
