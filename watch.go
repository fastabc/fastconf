package fastconf

// Subscribe registers a callback that fires on every successful reload,
// receiving the extracted M from the previous and new *T. Use it when you
// want type-safe access to a struct field (or sub-struct) of *T without
// reaching for reflection.
//
//	cancel := fastconf.Subscribe(mgr,
//	    func(c *AppConfig) *DBConfig { return &c.Database },
//	    func(old, new *DBConfig) {
//	        if old != nil && *old == *new {
//	            return // no real change in DBConfig — caller-side filter
//	        }
//	        reconnect(new)
//	    },
//	)
//	defer cancel()
//
// The callback fires unconditionally on every commit; if the extracted M
// is unchanged the caller is responsible for skipping (typical pattern:
// compare old and new). This keeps Subscribe O(0) on the reload hot path
// — no per-field hashing — and lets the caller decide what "changed"
// means for their type.
//
// Callbacks run synchronously on the reload goroutine. They must return
// quickly; any blocking I/O (RPC, lock contention, time.Sleep) postpones
// the next reload. Spawn a goroutine inside the callback if needed.
//
// A panic in fn is recovered and logged; it does not poison the writer
// or affect other subscribers. The returned cancel removes the
// subscription; calling it after Close() is a no-op.
func Subscribe[T any, M any](m *Manager[T], extract func(*T) *M, fn func(old, new *M)) (cancel func()) {
	if m == nil || extract == nil || fn == nil {
		return func() {}
	}
	wrapper := func(prev, next *State[T]) {
		var oldV, newV *M
		if prev != nil && prev.Value != nil {
			oldV = extract(prev.Value)
		}
		if next != nil && next.Value != nil {
			newV = extract(next.Value)
		}
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
	fn func(prev, next *State[T])
}

// fireWatches dispatches every subscriber after a successful commit.
// Each callback runs synchronously inside the reload goroutine, with a
// recover() guard so a misbehaving subscriber never poisons the writer
// or affects other subscribers.
func (m *Manager[T]) fireWatches(prev, next *State[T]) {
	if next == nil {
		return
	}
	m.watchMu.Lock()
	if len(m.watches) == 0 {
		m.watchMu.Unlock()
		return
	}
	subs := make([]*subscriber[T], 0, len(m.watches))
	for _, w := range m.watches {
		subs = append(subs, w)
	}
	m.watchMu.Unlock()
	for _, w := range subs {
		safeFire(m, w.fn, prev, next)
	}
}

func safeFire[T any](m *Manager[T], fn func(prev, next *State[T]), prev, next *State[T]) {
	defer func() {
		if r := recover(); r != nil {
			m.opts.log.Error().Any("panic", r).Msg("subscribe callback panic")
		}
	}()
	fn(prev, next)
}
