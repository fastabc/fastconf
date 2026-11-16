package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
)

type fakeNotifier struct {
	runs       atomic.Int32
	trigger    chan struct{}
	results    chan error
	stopped    chan struct{}
	failFirst  error
	panicFirst bool
}

func (n *fakeNotifier) Run(ctx context.Context, refresh func(context.Context) error) error {
	run := n.runs.Add(1)
	if run == 1 && n.panicFirst {
		panic("boom")
	}
	if run == 1 && n.failFirst != nil {
		return n.failFirst
	}
	defer close(n.stopped)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.trigger:
			err := refresh(ctx)
			select {
			case n.results <- err:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

type namedNotifier struct{ *fakeNotifier }

func (namedNotifier) Name() string { return "sns" }
func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{trigger: make(chan struct{}, 1), results: make(chan error, 1), stopped: make(chan struct{})}
}
func TestNotifier_RefreshReloadsAndReturnsResult(t *testing.T) {
	for _, name := range []string{"unnamed", "named"} {
		t.Run(name, func(t *testing.T) {
			n := newFakeNotifier()
			p := &loadOnlyProvider{}
			p.value.Store(1)
			opt := fastconf.WithNotifier(n)
			reason := "notify[0]"
			if name == "named" {
				opt = fastconf.WithNotifier(namedNotifier{n})
				reason = "notify:sns"
			}
			m, err := fastconf.New[struct {
				Value int `json:"value"`
			}](context.Background(), fastconf.WithProvider(p), opt)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			p.value.Store(2)
			n.trigger <- struct{}{}
			if err := await(t, n.results); err != nil {
				t.Fatal(err)
			}
			if m.Get().Value != 2 || m.Snapshot().Cause().Reason != reason {
				t.Fatalf("%+v %+v", m.Get(), m.Snapshot().Cause())
			}
			m.Pause()
			g := m.Snapshot().Generation()
			p.value.Store(3)
			n.trigger <- struct{}{}
			if err := await(t, n.results); !errors.Is(err, fastconf.ErrPaused) || !errors.Is(err, fastconf.ErrFastConf) {
				t.Fatalf("paused refresh = %v; want ErrPaused", err)
			}
			if m.Snapshot().Generation() != g {
				t.Fatal("reloaded while paused")
			}
			m.Resume()
			n.trigger <- struct{}{} // Redelivery after the failed acknowledgement.
			if err := await(t, n.results); err != nil {
				t.Fatal(err)
			}
			if m.Get().Value != 3 {
				t.Fatal("redelivery did not refresh")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-n.stopped:
			default:
				t.Fatal("notifier still running")
			}
		})
	}
}
func TestNotifier_RestartsAfterError(t *testing.T) {
	for _, name := range []string{"error", "panic"} {
		t.Run(name, func(t *testing.T) {
			n := newFakeNotifier()
			n.failFirst = errors.New("boom")
			n.panicFirst = name == "panic"
			obs := &recObserver{}
			m, err := fastconf.New[struct {
				Value int `json:"value"`
			}](context.Background(), fastconf.WithProvider(&loadOnlyProvider{}), fastconf.WithNotifier(n), fastconf.WithObserver(obs))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			waitFor(t, func() bool { return n.runs.Load() >= 2 }, "notifier restart")
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, ev := range obs.snapshot() {
				if e, ok := ev.(fastconf.ProviderError); ok && e.Provider == "notify[0]" && e.Err != nil {
					found = true
				}
			}
			if !found {
				t.Fatal("missing ProviderError")
			}
		})
	}
}
func TestWithResync_RecoversMissedChange(t *testing.T) {
	p := &loadOnlyProvider{}
	p.value.Store(1)
	m, err := fastconf.New[struct {
		Value int `json:"value"`
	}](context.Background(), fastconf.WithProvider(p), fastconf.WithResync(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	p.value.Store(2)
	waitFor(t, func() bool { return m.Get().Value == 2 }, "resync")
	if m.Snapshot().Cause().Reason != "resync" {
		t.Fatal(m.Snapshot().Cause())
	}
}
func TestNotifyOptions_Reject(t *testing.T) {
	for _, opt := range []fastconf.Option{fastconf.WithNotifier(nil), fastconf.WithResync(-time.Second)} {
		m, err := fastconf.New[struct{}](context.Background(), opt)
		if m != nil {
			_ = m.Close()
		}
		if !errors.Is(err, fastconf.ErrFastConf) {
			t.Fatal(err)
		}
	}
}
func TestLoad_IgnoresNotifiers(t *testing.T) {
	n := newFakeNotifier()
	_, err := fastconf.Load[struct {
		Value int `json:"value"`
	}](context.Background(), fastconf.WithProvider(&loadOnlyProvider{}), fastconf.WithNotifier(n), fastconf.WithResync(time.Nanosecond))
	if err != nil || n.runs.Load() != 0 {
		t.Fatalf("%v %d", err, n.runs.Load())
	}
}

func TestNotifier_RefreshFailurePreservesState(t *testing.T) {
	n := newFakeNotifier()
	p := &snapshotProvider{name: "p", data: map[string]any{"value": 1}}
	m, err := fastconf.New[struct {
		Value int `json:"value"`
	}](context.Background(), fastconf.WithProvider(p), fastconf.WithNotifier(n))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	p.data = map[string]any{"value": "invalid"}
	n.trigger <- struct{}{}
	if err := await(t, n.results); !errors.Is(err, fastconf.ErrTransform) {
		t.Fatalf("refresh error = %v", err)
	}
	if m.Snapshot() != before {
		t.Fatal("failed refresh published state")
	}
}
