package fastconf

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"slices"
)

// SubscribeOption customises a [Subscribe] registration. The only
// constructor today is [WithEqual]; the type is exported so callers can
// write helper functions that return options.
type SubscribeOption[S any] func(*subscribeOpts[S])

// subscribeOpts carries the resolved per-subscriber knobs.
type subscribeOpts[S any] struct {
	equal func(old, new *S) bool
}

// WithEqual replaces the default [reflect.DeepEqual] comparator used by
// [Subscribe] to decide whether the extracted value actually changed.
//
// The framework invokes equal only with two non-nil pointers; nil ↔
// non-nil transitions are unambiguous changes and never consult equal.
// Return true to mark old and new as unchanged (the callback is skipped).
//
// Common uses:
//
//   - Ignore a noisy field: return a.URL == b.URL && a.Pool == b.Pool
//   - Hash-compare large structs: return a.Hash == b.Hash
//   - React to every commit while both extracted values are non-nil:
//     WithEqual(func(_, _ *T) bool { return false })
//
// Equal configuration hashes skip publication before subscribers run. To observe
// every reload attempt, including unchanged results, use WithObserver and ReloadFinished.
func WithEqual[S any](equal func(old, new *S) bool) SubscribeOption[S] {
	return func(o *subscribeOpts[S]) { o.equal = equal }
}

// Subscribe registers a callback that fires after a commit (including rollback)
// when the value extracted by extract has actually changed.
//
// Change detection uses [reflect.DeepEqual] on the dereferenced values by
// default. Pass [WithEqual] to substitute a custom comparator.
//
//	cancel := fastconf.Subscribe(mgr,
//	    func(c *AppConfig) *DBConfig { return &c.Database },
//	    func(old, new *DBConfig) {
//	        reconnect(new) // guaranteed: database config actually changed
//	    },
//	)
//	defer cancel()
//
// nil ↔ non-nil transitions always fire (equal is not consulted); two nil
// values do not fire.
//
// Callbacks run synchronously in registration order on the reload goroutine.
// They must return quickly; blocking I/O postpones the next reload. Spawn a goroutine
// inside the callback if needed.
//
// A panic in extract, fn or a [WithEqual] comparator is recovered and
// surfaced on [Manager.Errors]; it does not poison the writer or affect
// other subscribers. The returned cancel removes the subscription;
// calling it after Close() is a no-op.
func Subscribe[T any, S any](
	m *Manager[T],
	extract func(*T) *S,
	fn func(old, new *S),
	opts ...SubscribeOption[S],
) (cancel func()) {
	if m == nil || extract == nil || fn == nil {
		return func() {}
	}
	var cfg subscribeOpts[S]
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	equal := cfg.equal
	wrapper := func(prev, next *State[T]) {
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
	id := m.subscriberSeq.Add(1)
	m.subscriberMu.Lock()
	m.subscribers[id] = wrapper
	m.subscriberMu.Unlock()
	return func() {
		m.subscriberMu.Lock()
		delete(m.subscribers, id)
		m.subscriberMu.Unlock()
	}
}

// subscriber compares extracted values and invokes one user callback.
type subscriber[T any] func(prev, next *State[T])

// notifySubscribers dispatches every subscriber after a successful commit.
// Each callback runs synchronously inside the reload goroutine, with a
// recover() guard so a misbehaving subscriber never poisons the writer
// or affects other subscribers.
func (m *Manager[T]) notifySubscribers(prev, next *State[T]) {
	if next == nil {
		return
	}
	m.subscriberMu.RLock()
	if len(m.subscribers) == 0 {
		m.subscriberMu.RUnlock()
		return
	}
	type keyed struct {
		id uint64
		fn subscriber[T]
	}
	subs := make([]keyed, 0, len(m.subscribers))
	for id, sub := range m.subscribers {
		subs = append(subs, keyed{id, sub})
	}
	m.subscriberMu.RUnlock()
	slices.SortFunc(subs, func(a, b keyed) int { return cmp.Compare(a.id, b.id) })
	for _, s := range subs {
		callSubscriber(m, s.fn, prev, next)
	}
}

func callSubscriber[T any](m *Manager[T], fn func(prev, next *State[T]), prev, next *State[T]) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := debug.Stack()
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelError, "subscribe callback panic", slog.Any("panic", r), slog.String("stack", string(stack)))
		// Surface the panic on the Errors() channel so consumers that
		// already centralise reload-failure handling there see
		// subscriber failures too. The panic does NOT abort the reload
		// (the new state has already been published); it is reported
		// for observability only.
		m.publishReloadError("subscriber-panic", fmt.Errorf("subscriber panic: %v", r))
	}()
	fn(prev, next)
}
