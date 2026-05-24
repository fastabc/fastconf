package manager

import (
	"fmt"
	"reflect"
	"runtime/debug"

	istate "github.com/fastabc/fastconf/internal/state"
)

// Subscribe registers a callback that fires after a successful reload when
// the value extracted by extract has actually changed. The root facade
// resolves any user-supplied [fastconf.WithEqual] option into the trailing
// equal argument; passing nil here means "use [reflect.DeepEqual] on the
// dereferenced values".
//
// Behavior:
//
//   - Both nil   → callback skipped.
//   - nil ↔ non-nil → callback fires (equality function not invoked).
//   - Both non-nil → equality function (or DeepEqual fallback) decides:
//     true means "unchanged, skip"; false means "changed, fire".
//
// Callbacks run synchronously on the reload goroutine. They must return
// quickly; blocking I/O postpones the next reload. Spawn a goroutine
// inside the callback if needed.
//
// A panic in fn (or in equal) is recovered, logged, and surfaced on the
// manager's Errors() channel; it does not poison the writer or affect
// other subscribers. The returned cancel removes the subscription;
// calling it after Close() is a no-op.
func Subscribe[T any, S any](
	m *M[T],
	extract func(*T) *S,
	fn func(old, new *S),
	equal func(old, new *S) bool,
) (cancel func()) {
	if m == nil || extract == nil || fn == nil {
		return func() {}
	}
	wrapper := func(prev, next *istate.State[T]) {
		var oldV, newV *S
		if prev != nil && prev.Value() != nil {
			oldV = extract(prev.Value())
		}
		if next != nil && next.Value() != nil {
			newV = extract(next.Value())
		}
		// Both nil — nothing to compare, nothing changed.
		if oldV == nil && newV == nil {
			return
		}
		// Both present — defer to equal (or DeepEqual fallback).
		if oldV != nil && newV != nil {
			if equal != nil {
				if equal(oldV, newV) {
					return
				}
			} else if reflect.DeepEqual(*oldV, *newV) {
				return
			}
		}
		// nil <-> non-nil transitions fall through and fire.
		fn(oldV, newV)
	}
	id := m.watchSeq.Add(1)
	m.watchMu.Lock()
	m.watches[id] = &subscriber[T]{fn: wrapper}
	m.watchMu.Unlock()
	return func() {
		m.watchMu.Lock()
		delete(m.watches, id)
		m.watchMu.Unlock()
	}
}

// subscriber holds one user subscription.
type subscriber[T any] struct {
	fn func(prev, next *istate.State[T])
}

// fireWatches dispatches every subscriber after a successful commit.
// Each callback runs synchronously inside the reload goroutine, with a
// recover() guard so a misbehaving subscriber never poisons the writer
// or affects other subscribers.
func (m *M[T]) fireWatches(prev, next *istate.State[T]) {
	if next == nil {
		return
	}
	m.watchMu.RLock()
	if len(m.watches) == 0 {
		m.watchMu.RUnlock()
		return
	}
	subs := make([]*subscriber[T], 0, len(m.watches))
	for _, w := range m.watches {
		subs = append(subs, w)
	}
	m.watchMu.RUnlock()
	for _, w := range subs {
		safeFire(m, w.fn, prev, next)
	}
}

func safeFire[T any](m *M[T], fn func(prev, next *istate.State[T]), prev, next *istate.State[T]) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := debug.Stack()
		m.opts.Log.Error().
			Any("panic", r).
			Str("stack", string(stack)).
			Msg("subscribe callback panic")
		// Surface the panic on the Errors() channel so consumers that
		// already centralise reload-failure handling there see
		// subscriber failures too. The panic does NOT abort the reload
		// (the new state has already been published); it is reported
		// for observability only.
		m.publishReloadError("subscriber-panic", fmt.Errorf("subscriber panic: %v", r))
	}()
	fn(prev, next)
}
