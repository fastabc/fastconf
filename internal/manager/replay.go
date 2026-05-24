package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
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

	req := reloadRequest{
		reason: "rollback",
		applyFn: func(_ context.Context) error {
			return m.applyRollback(target)
		},
		doneCh: make(chan error, 1),
	}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return fcerr.ErrClosed
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return fcerr.ErrClosed
	}
}

func (m *M[T]) applyRollback(target *istate.State[T]) error {
	prev := m.state.Load()
	m.state.Store(target)
	for {
		cur := m.gen.Load()
		next := target.Generation() + 1
		if cur >= next {
			break
		}
		if m.gen.CompareAndSwap(cur, next) {
			break
		}
	}
	if prev != nil {
		m.opts.Log.Info().
			Uint64("from", prev.Generation()).
			Uint64("to", target.Generation()).
			Msg("fastconf rollback")
	}
	m.fireWatches(prev, target)
	return nil
}

func (m *M[T]) Watcher() *Watcher[T] { return (*Watcher[T])(m) }

type Watcher[T any] M[T]

func (w *Watcher[T]) Pause()       { (*M[T])(w).watchPaused.Store(true) }
func (w *Watcher[T]) Resume()      { (*M[T])(w).watchPaused.Store(false) }
func (w *Watcher[T]) Paused() bool { return (*M[T])(w).watchPaused.Load() }
