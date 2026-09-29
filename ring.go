package fastconf

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
