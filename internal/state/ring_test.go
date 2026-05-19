package state

import "testing"

func TestRingBuffer_CircularPush(t *testing.T) {
	r := NewRing[State[int]](3)
	mk := func(g uint64) *State[int] { return &State[int]{Generation: g} }
	for g := uint64(1); g <= 5; g++ {
		r.Push(mk(g))
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3, got %d", len(snap))
	}
	want := []uint64{3, 4, 5}
	for i, s := range snap {
		if s.Generation != want[i] {
			t.Fatalf("idx %d: want %d got %d", i, want[i], s.Generation)
		}
	}
	matchGen := func(g uint64) func(*State[int]) bool {
		return func(s *State[int]) bool { return s.Generation == g }
	}
	if r.Find(matchGen(4)) == nil || r.Find(matchGen(1)) != nil {
		t.Fatalf("Find broken")
	}
}
