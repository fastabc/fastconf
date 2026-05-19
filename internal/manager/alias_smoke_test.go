package manager

import (
	"sync/atomic"
	"testing"
)

type smokeM[T any] struct {
	v atomic.Pointer[T]
}

func (m *smokeM[T]) Store(v *T) { m.v.Store(v) }
func (m *smokeM[T]) Load() *T   { return m.v.Load() }

type smokeSub[T any] smokeM[T]

func (s *smokeSub[T]) LoadSub() *T { return (*smokeM[T])(s).Load() }

func TestDefinedSubtypeSmoke(t *testing.T) {
	var m smokeM[int]
	v := 42
	m.Store(&v)
	sub := (*smokeSub[int])(&m)
	if got := sub.LoadSub(); got == nil || *got != 42 {
		t.Fatalf("sub LoadSub() = %v", got)
	}
}
