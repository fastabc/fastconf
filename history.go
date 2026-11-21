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

// Rollback is RollbackContext with context.Background(): it waits for the
// writer however long earlier queued work takes.
func (r *History[T]) Rollback(target *State[T]) error {
	return r.RollbackContext(context.Background(), target)
}

// RollbackContext republishes target as a new generation. target must be one of
// the snapshots List returns and still be retained when the writer executes the
// request. Concurrent commits can evict it; in that case ErrUnknownGeneration
// is returned without publication. Rollback restores memory, not source files.
//
// ctx bounds the wait for the writer, which may be busy with an earlier reload
// such as a slow provider Load. If ctx ends before the writer starts the
// rollback, it is not applied and ctx's error is returned. Publication itself
// is not interruptible: once it begins it completes, as for Reload, so a
// context error that races the end of publication does not prove the rollback
// was skipped; compare Snapshot().Generation when that matters.
func (r *History[T]) RollbackContext(ctx context.Context, target *State[T]) error {
	if r == nil {
		return ErrHistoryDisabled
	}
	m := (*Manager[T])(r)
	if target == nil {
		return fmt.Errorf("%w: nil target", ErrUnknownGeneration)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return m.submitAndWait(ctx, reloadRequest{
		ctx:     ctx,
		trigger: TriggerRollback,
		reason:  "rollback",
		// runReloadRequest skips requests whose context ended while queued.
		// applyRollback does no I/O before publishing, so it needs no further check.
		run: func(context.Context) error {
			return m.applyRollback(target)
		},
	})
}

func (m *Manager[T]) applyRollback(target *State[T]) error {
	// Check membership on the writer, not before enqueueing: a preceding commit
	// may have evicted the target while this request waited in the queue.
	m.historyMu.Lock()
	found := m.history.Find(func(s *State[T]) bool { return s == target })
	m.historyMu.Unlock()
	if found == nil {
		return fmt.Errorf("%w: generation %d not in history", ErrUnknownGeneration, target.Generation())
	}
	prev := m.state.Load()
	cause := ReloadCause{
		Reason:  "rollback",
		Trigger: TriggerRollback,
		At:      time.Now(),
		Tenant:  m.tenant,
	}
	next := restampState(target, m.gen.Add(1), cause)
	// The live state no longer reflects the last reload's inputs.
	m.publishedFingerprint = [32]byte{}
	m.publishSnapshot(prev, next, "rollback")
	m.notifySubscribers(prev, next)
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

// newRing returns a fresh ring with the given capacity. cap <= 0 yields
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

// Find returns the first item satisfying pred (oldest first). Rollback uses
// it to check the target's membership by pointer identity without allocating
// a snapshot slice. Returns nil when no item matches or pred is nil.
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
