package debounce

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDebouncer_CoalescesBurst(t *testing.T) {
	var fired atomic.Int64
	var lastReason atomic.Value
	d := New(50*time.Millisecond, func(reason string) {
		fired.Add(1)
		lastReason.Store(reason)
	})
	defer d.Stop()
	for i := 0; i < 10; i++ {
		d.Trigger("a")
		time.Sleep(5 * time.Millisecond)
	}
	d.Trigger("b")
	time.Sleep(150 * time.Millisecond)
	if fired.Load() != 1 {
		t.Errorf("expected 1 fire, got %d", fired.Load())
	}
	r, _ := lastReason.Load().(string)
	if r != "a,b" {
		t.Errorf("reasons = %q", r)
	}
}

func TestDebouncer_StopPreventsFire(t *testing.T) {
	var fired atomic.Int64
	d := New(20*time.Millisecond, func(string) { fired.Add(1) })
	d.Trigger("x")
	d.Stop()
	time.Sleep(50 * time.Millisecond)
	if fired.Load() != 0 {
		t.Errorf("expected 0 fires after Stop, got %d", fired.Load())
	}
}
