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
	for _, named := range []bool{false, true} {
		n := newFakeNotifier()
		p := &loadOnlyProvider{}
		p.value.Store(1)
		opt := fastconf.WithNotifier(n)
		reason := "notify[0]"
		if named {
			opt = fastconf.WithNotifier(namedNotifier{n})
			reason = "notify:sns"
		}
		m, err := fastconf.New[struct {
			Value int `json:"value"`
		}](context.Background(), fastconf.WithProvider(p), opt)
		if err != nil {
			t.Fatal(err)
		}
		p.value.Store(2)
		n.trigger <- struct{}{}
		select {
		case err := <-n.results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("refresh timed out")
		}
		if m.Get().Value != 2 || m.Snapshot().Cause().Reason != reason {
			t.Fatalf("%+v %+v", m.Get(), m.Snapshot().Cause())
		}
		m.Pause()
		g := m.Snapshot().Generation()
		p.value.Store(3)
		n.trigger <- struct{}{}
		select {
		case err := <-n.results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("pause timed out")
		}
		if m.Snapshot().Generation() != g {
			t.Fatal("reloaded while paused")
		}
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-n.stopped:
		default:
			t.Fatal("notifier still running")
		}
	}
}
func TestNotifier_RestartsAfterError(t *testing.T) {
	for _, panics := range []bool{false, true} {
		n := newFakeNotifier()
		n.failFirst = errors.New("boom")
		n.panicFirst = panics
		obs := &recObserver{}
		m, err := fastconf.New[struct {
			Value int `json:"value"`
		}](context.Background(), fastconf.WithProvider(&loadOnlyProvider{}), fastconf.WithNotifier(n), fastconf.WithObserver(obs))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.After(2 * time.Second)
		for n.runs.Load() < 2 {
			select {
			case <-deadline:
				t.Fatal("did not restart")
			case <-time.After(5 * time.Millisecond):
			}
		}
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
	deadline := time.After(time.Second)
	for m.Get().Value != 2 {
		select {
		case <-deadline:
			t.Fatal("resync timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if m.Snapshot().Cause().Reason != "resync" {
		t.Fatal(m.Snapshot().Cause())
	}
}
func TestNotifyOptions_Reject(t *testing.T) {
	for _, opt := range []fastconf.Option{fastconf.WithNotifier(nil), fastconf.WithResync(-time.Second)} {
		_, err := fastconf.New[struct{}](context.Background(), opt)
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
	select {
	case err := <-n.results:
		if !errors.Is(err, fastconf.ErrTransform) {
			t.Fatalf("refresh error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not return")
	}
	if m.Snapshot() != before {
		t.Fatal("failed refresh published state")
	}
}
