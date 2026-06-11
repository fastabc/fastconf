package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	istate "github.com/fastabc/fastconf/internal/state"
)

var ErrUnknownGeneration = errors.New("fastconf: unknown generation")
var ErrHistoryDisabled = errors.New("fastconf: history disabled")

func (m *M[T]) Replay() *Replay[T] { return (*Replay[T])(m) }

type Replay[T any] M[T]

func (r *Replay[T]) List() []*istate.State[T] {
	m := (*M[T])(r)
	if m.history == nil {
		return nil
	}
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	return m.history.Snapshot()
}

func (r *Replay[T]) Rollback(target *istate.State[T]) error {
	m := (*M[T])(r)
	if m.history == nil {
		return ErrHistoryDisabled
	}
	if target == nil {
		return fmt.Errorf("%w: nil target", ErrUnknownGeneration)
	}
	m.historyMu.Lock()
	found := m.history.Find(func(s *istate.State[T]) bool { return s.Generation() == target.Generation() })
	m.historyMu.Unlock()
	if found != target {
		return fmt.Errorf("%w: generation %d not in history", ErrUnknownGeneration, target.Generation())
	}

	return m.enqueue(context.Background(), reloadRequest{
		reason: "rollback",
		applyFn: func(_ context.Context) error {
			return m.applyRollback(target)
		},
		doneCh: make(chan error, 1),
	})
}

func (m *M[T]) applyRollback(target *istate.State[T]) error {
	prev := m.state.Load()
	cause := istate.ReloadCause{
		Reason: "rollback",
		At:     time.Now().UnixNano(),
		Tenant: m.tenant,
	}
	ns := istate.Restamp(target, m.gen.Add(1), cause)
	m.publishSnapshot(prev, ns, "rollback")
	m.fanoutAfterPublish(prev, ns, "rollback")
	if prev != nil {
		m.opts.Log.Info().
			Uint64("from", prev.Generation()).
			Uint64("to", ns.Generation()).
			Msg("fastconf rollback")
	}
	return nil
}

func (m *M[T]) Watcher() *Watcher[T] { return (*Watcher[T])(m) }

type Watcher[T any] M[T]

func (w *Watcher[T]) Pause()       { (*M[T])(w).watchPaused.Store(true) }
func (w *Watcher[T]) Resume()      { (*M[T])(w).watchPaused.Store(false) }
func (w *Watcher[T]) Paused() bool { return (*M[T])(w).watchPaused.Load() }
