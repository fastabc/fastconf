package fastconf

import (
	"context"
	"errors"
	"testing"

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

	if err := mgr.History().Rollback(beta); err != nil {
		t.Fatalf("rollback beta: %v", err)
	}
	rolled := mgr.Snapshot()
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
