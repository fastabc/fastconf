package fastconf

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

var ErrUnknownGeneration = fcerr.New("fastconf: unknown generation")
var ErrHistoryDisabled = fcerr.New("fastconf: history disabled")

// History exposes the snapshots retained by WithHistory. It returns nil
// when WithHistory was not configured.
func (m *Manager[T]) History() *History[T] {
	if m == nil || m.history == nil {
		return nil
	}
	return (*History[T])(m)
}

// History lists and restores retained snapshots. Obtain it from
// Manager.History.
type History[T any] Manager[T]

// List returns the retained snapshots, oldest first. The current snapshot
// is not included.
func (r *History[T]) List() []*State[T] {
	if r == nil {
		return nil
	}
	m := (*Manager[T])(r)
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	return m.history.Snapshot()
}

// Rollback republishes target as a new generation. target must be one of
// the snapshots List returns.
func (r *History[T]) Rollback(target *State[T]) error {
	if r == nil {
		return ErrHistoryDisabled
	}
	m := (*Manager[T])(r)
	if target == nil {
		return fmt.Errorf("%w: nil target", ErrUnknownGeneration)
	}
	m.historyMu.Lock()
	found := m.history.Find(func(s *State[T]) bool { return s.Generation() == target.Generation() })
	m.historyMu.Unlock()
	if found != target {
		return fmt.Errorf("%w: generation %d not in history", ErrUnknownGeneration, target.Generation())
	}

	return m.enqueue(context.Background(), reloadRequest{
		reason: "rollback",
		applyFn: func(_ context.Context) error {
			return m.applyRollback(target)
		},
	})
}

func (m *Manager[T]) applyRollback(target *State[T]) error {
	prev := m.state.Load()
	cause := ReloadCause{
		Reason: "rollback",
		At:     time.Now().UnixNano(),
		Tenant: m.tenant,
	}
	next := restampState(target, m.gen.Add(1), cause)
	// The live state no longer reflects the last reload's inputs.
	m.lastInputs = [32]byte{}
	m.publishSnapshot(prev, next, "rollback")
	if prev != nil {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelInfo, "fastconf rollback", slog.Uint64("from", prev.Generation()), slog.Uint64("to", next.Generation()))
	}
	return nil
}
