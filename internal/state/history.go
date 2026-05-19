package state

// Ring is a fixed-size FIFO of pointers used by Manager to retain recent
// snapshots for rollback. The original ringBuffer in root fastconf was
// parametrized over the user config type T (storing *State[T]); here it
// is parametrized over the *item* type E directly so internal/state has
// no upward dependency on root.
//
// Typical instantiation from root:
//
//	import istate "github.com/fastabc/fastconf/internal/state"
//	ring := istate.NewRing[State[T]](historyCap)
//	ring.Push(&State[T]{...})
//
// Ring is not safe for concurrent use; callers (Manager.historyMu)
// synchronise access.
type Ring[E any] struct {
	cap   int
	items []*E
	head  int
	size  int
}

// NewRing returns a fresh Ring with the given capacity. cap <= 0 yields
// nil so callers can treat history as a nil-safe opt-in.
func NewRing[E any](cap int) *Ring[E] {
	if cap <= 0 {
		return nil
	}
	return &Ring[E]{cap: cap, items: make([]*E, cap)}
}

// Push inserts item at the tail. When the ring is full the oldest
// element is evicted (wrap-around).
func (r *Ring[E]) Push(item *E) {
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
func (r *Ring[E]) Snapshot() []*E {
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
func (r *Ring[E]) Find(pred func(*E) bool) *E {
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
