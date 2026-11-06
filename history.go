package fastconf

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// ErrUnknownGeneration means a rollback target is not a retained snapshot.
var ErrUnknownGeneration = fcerr.New("fastconf: unknown generation")

// ErrHistoryDisabled means history retention is not enabled.
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

	return m.submitAndWait(context.Background(), reloadRequest{
		reason: "rollback",
		run: func(_ context.Context) error {
			return m.applyRollback(target)
		},
	})
}

func (m *Manager[T]) applyRollback(target *State[T]) error {
	prev := m.state.Load()
	cause := ReloadCause{
		Reason: "rollback",
		At:     time.Now(),
		Tenant: m.tenant,
	}
	next := restampState(target, m.gen.Add(1), cause)
	// The live state no longer reflects the last reload's inputs.
	m.publishedFingerprint = [32]byte{}
	m.publishSnapshot(prev, next, "rollback")
	if prev != nil {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelInfo, "fastconf rollback", slog.Uint64("from", prev.Generation()), slog.Uint64("to", next.Generation()))
	}
	return nil
}

// ring is a fixed-size FIFO of pointers used by Manager to retain recent
// snapshots for rollback (ring[State[T]]). It is not safe for concurrent
// use; callers (Manager.historyMu) synchronise access.
type ring[E any] struct {
	cap   int
	items []*E
	head  int
	size  int
}

// newRing returns a fresh Ring with the given capacity. cap <= 0 yields
// nil so callers can treat history as a nil-safe opt-in.
func newRing[E any](cap int) *ring[E] {
	if cap <= 0 {
		return nil
	}
	return &ring[E]{cap: cap, items: make([]*E, cap)}
}

// Push inserts item at the tail. When the ring is full the oldest
// element is evicted (wrap-around).
func (r *ring[E]) Push(item *E) {
	if r == nil {
		return
	}
	tail := (r.head + r.size) % r.cap
	r.items[tail] = item
	if r.size < r.cap {
		r.size++
	} else {
		r.head = (r.head + 1) % r.cap
	}
}

// Snapshot returns a freshly allocated slice with the live items,
// oldest first. Callers may iterate without holding the ring lock.
func (r *ring[E]) Snapshot() []*E {
	if r == nil {
		return nil
	}
	out := make([]*E, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.items[(r.head+i)%r.cap]
	}
	return out
}

// Find returns the first item satisfying pred (oldest first). Used by
// rollback to locate a target State by Generation without allocating a
// snapshot slice. Returns nil when no item matches or pred is nil.
func (r *ring[E]) Find(pred func(*E) bool) *E {
	if r == nil || pred == nil {
		return nil
	}
	for i := 0; i < r.size; i++ {
		s := r.items[(r.head+i)%r.cap]
		if s != nil && pred(s) {
			return s
		}
	}
	return nil
}
