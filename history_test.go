package fastconf

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

func TestHistory_RejectsUnknownSnapshot(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: gen1\n"))),
		WithHistory(3),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if hist := mgr.History().List(); len(hist) != 0 {
		t.Fatalf("history=%d want 0 (no prior commit)", len(hist))
	}

	// Rollback to a fake snapshot with a generation that doesn't exist.
	fake := &State[snapshotConfig]{generation: 999}
	if err := mgr.History().Rollback(fake); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("rollback unknown gen err=%v", err)
	}
	if err := mgr.History().Rollback(nil); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("rollback nil err=%v want ErrUnknownGeneration", err)
	}
}

func TestHistory_RollbackPublishesFanoutAndCanRollForward(t *testing.T) {
	committed := make(chan Committed, 16)
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("a", "yaml", []byte("name: alpha\n"))),
		WithHistory(4),
		WithObserver(observerFunc(func(_ context.Context, e Event) {
			if c, ok := e.(Committed); ok {
				committed <- c
			}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	first := mgr.Snapshot()
	if err := mgr.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	if got := mgr.History().List(); len(got) != 1 || got[0] != first {
		t.Fatalf("history = %v; want [first]", got)
	}
	if err := mgr.Reload(context.Background(), WithOverride(map[string]any{"name": "gamma"})); err != nil {
		t.Fatal(err)
	}
	before := mgr.Snapshot()
	var beta *State[snapshotConfig]
	for _, s := range mgr.History().List() {
		if s.Value().Name == "beta" {
			beta = s
			break
		}
	}
	if beta == nil {
		t.Fatalf("history missing beta snapshot: %+v", mgr.History().List())
	}

	rollbackStarted := time.Now()
	if err := mgr.History().Rollback(beta); err != nil {
		t.Fatalf("rollback beta: %v", err)
	}
	rolled := mgr.Snapshot()
	if at := rolled.Cause().At; at.Before(rollbackStarted) || at.After(time.Now()) {
		t.Fatalf("rollback timestamp = %v; want a fresh commit time", at)
	}
	if rolled.Value().Name != "beta" {
		t.Fatalf("rollback value=%q want beta", rolled.Value().Name)
	}
	if rolled.Generation() <= before.Generation() {
		t.Fatalf("rollback generation did not advance: before=%d after=%d", before.Generation(), rolled.Generation())
	}
	if rolled.Cause().Reason != "rollback" {
		t.Fatalf("rollback cause=%q", rolled.Cause().Reason)
	}
	if ev := waitCommittedReason(t, committed, "rollback"); ev.Next != rolled.Generation() || len(ev.Diff()) == 0 {
		t.Fatalf("rollback Committed = %+v (diff %v); want generation %d with a diff", ev, ev.Diff(), rolled.Generation())
	}

	var gamma *State[snapshotConfig]
	for _, s := range mgr.History().List() {
		if s.Value().Name == "gamma" {
			gamma = s
			break
		}
	}
	if gamma == nil {
		t.Fatalf("history missing rolled-back gamma snapshot: %+v", mgr.History().List())
	}
	if err := mgr.History().Rollback(gamma); err != nil {
		t.Fatalf("roll-forward gamma: %v", err)
	}
	if got := mgr.Snapshot().Value().Name; got != "gamma" {
		t.Fatalf("roll-forward value=%q want gamma", got)
	}
}

func TestHistory_Disabled(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: x\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if mgr.History() != nil {
		t.Fatal("History must be nil when disabled")
	}
	if hist := mgr.History().List(); hist != nil {
		t.Fatalf("History().List() = %v; want nil when history disabled", hist)
	}
	fake := &State[snapshotConfig]{generation: 1}
	if err := mgr.History().Rollback(fake); !errors.Is(err, ErrHistoryDisabled) {
		t.Fatalf("Rollback() err=%v want ErrHistoryDisabled", err)
	}
}

func TestRollbackRechecksRetentionOnWriter(t *testing.T) {
	m, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"gamma"}`))), WithHistory(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "alpha"})); err != nil {
		t.Fatal(err)
	}
	target := m.Snapshot()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot()
	callbacks := 0
	Subscribe(m, func(c *snapshotConfig) *string { return &c.Name }, func(_, _ *string) { callbacks++ })
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	m.reloadCh <- reloadRequest{run: func(context.Context) error { close(started); <-release; return nil }}
	<-started
	// The next queued reload evicts alpha before the rollback runs.
	reloaded := make(chan error, 1)
	m.reloadCh <- reloadRequest{reason: "manual", doneCh: reloaded}
	rolledBack := make(chan error, 1)
	go func() { rolledBack <- m.History().Rollback(target) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for len(m.reloadCh) != 2 {
		select {
		case <-timer.C:
			t.Fatal("rollback was not queued")
		default:
			runtime.Gosched()
		}
	}
	close(release)
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	if err := <-rolledBack; !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("stale rollback = %v; want ErrUnknownGeneration", err)
	}
	if got := m.Snapshot(); got.Value().Name != "gamma" || got.Generation() != before.Generation()+1 {
		t.Fatalf("stale rollback changed state: %+v", got)
	}
	if got := m.History().List(); len(got) != 1 || got[0] != before {
		t.Fatal("stale rollback changed history")
	}
	if callbacks != 1 {
		t.Fatalf("callbacks = %d; only the successful reload should notify", callbacks)
	}
}

// newRollbackFixture returns a manager whose history retains target, the
// snapshot before the live one.
func newRollbackFixture(t *testing.T) (m *Manager[snapshotConfig], target *State[snapshotConfig]) {
	t.Helper()
	m, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"alpha"}`))), WithHistory(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	target = m.Snapshot()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	return m, target
}

// blockWriter occupies the writer until the returned release is called.
func blockWriter(t *testing.T, m *Manager[snapshotConfig]) (release func()) {
	t.Helper()
	started, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(done) }) }
	t.Cleanup(release)
	m.reloadCh <- reloadRequest{run: func(context.Context) error {
		close(started)
		<-done
		return nil
	}}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not start the blocking request")
	}
	return release
}

// drainWriter waits until the writer has processed every request queued so far.
func drainWriter(t *testing.T, m *Manager[snapshotConfig]) {
	t.Helper()
	err := m.submitAndWait(context.Background(), reloadRequest{run: func(context.Context) error {
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
}
