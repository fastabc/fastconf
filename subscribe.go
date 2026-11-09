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
type SubscribeOption[S any] func(*subscribeConfig[S])

// subscribeConfig carries the resolved per-subscriber knobs.
type subscribeConfig[S any] struct {
	equal func(old, new *S) bool
}

// WithEqual replaces reflect.DeepEqual; true suppresses the callback. It is only
// called for two non-nil extracted pointers; nil transitions bypass it. Equal-value
// reloads skip subscribers, including security-metadata-only commits. Use WithObserver
// to observe reloads and metadata commits.
// See docs/cookbook/runtime/subscribe.md for comparator examples.
func WithEqual[S any](equal func(old, new *S) bool) SubscribeOption[S] {
	return func(o *subscribeConfig[S]) { o.equal = equal }
}

// Subscribe calls fn after commits, including rollback, when extract's value changes.
// It compares dereferenced values with reflect.DeepEqual unless WithEqual replaces it;
// nil transitions fire, while two nil values do not. See ExampleSubscribe.
//
// Callbacks run synchronously in registration order on the writer. Return quickly;
// use a bounded worker for I/O. Calling this manager's Reload, Plan, rollback, Close
// or Shutdown synchronously deadlocks. Panics in extract, fn or the comparator are
// recovered and reported on Manager.Errors without affecting other subscribers.
// The returned cancellation function removes the subscription and is safe after Close.
func Subscribe[T any, S any](
	m *Manager[T],
	extract func(*T) *S,
	fn func(old, new *S),
	opts ...SubscribeOption[S],
) (cancel func()) {
	if m == nil || extract == nil || fn == nil {
		return func() {}
	}
	var cfg subscribeConfig[S]
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
		callSubscriberRecovering(m, s.fn, prev, next)
	}
}

func callSubscriberRecovering[T any](m *Manager[T], fn func(prev, next *State[T]), prev, next *State[T]) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := debug.Stack()
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelError, "subscribe callback panic", slog.Any("panic", r), slog.String("stack", string(stack)))
		// Publication already succeeded; report the panic without rolling it back.
		m.publishReloadError("subscriber-panic", fmt.Errorf("subscriber panic: %v", r))
	}()
	fn(prev, next)
}
