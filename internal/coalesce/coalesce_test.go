package coalesce

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder captures (key, reason) calls in order, with timestamps.
type recorder struct {
	mu     sync.Mutex
	calls  []call
	signal chan struct{}
}

type call struct {
	at     time.Time
	key    string
	reason string
}

func newRecorder() *recorder {
	return &recorder{signal: make(chan struct{}, 64)}
}

func (r *recorder) fn(key, reason string) {
	r.mu.Lock()
	r.calls = append(r.calls, call{at: time.Now(), key: key, reason: reason})
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recorder) snapshot() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]call, len(r.calls))
	copy(out, r.calls)
	return out
}

// waitFor blocks until the recorder has at least n calls or timeout.
func (r *recorder) waitFor(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if r.count() >= n {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return r.count() >= n
		}
		select {
		case <-r.signal:
		case <-time.After(remaining):
			return r.count() >= n
		}
	}
}

func TestCoalesce_SingleEvent_FiresAfterQuiet(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: 40 * time.Millisecond, MaxLag: 400 * time.Millisecond, SwapHint: 5 * time.Millisecond}, r.fn)
	defer c.Stop()
	start := time.Now()
	c.Push("k1", "a", false)
	if !r.waitFor(1, 300*time.Millisecond) {
		t.Fatalf("timeout waiting for fire; count=%d", r.count())
	}
	calls := r.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].key != "k1" || calls[0].reason != "a" {
		t.Errorf("call = %+v", calls[0])
	}
	if d := calls[0].at.Sub(start); d < 30*time.Millisecond {
		t.Errorf("fired too early: %v", d)
	}
}

func TestCoalesce_BurstCollapsesToOne(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: 50 * time.Millisecond, MaxLag: 500 * time.Millisecond}, r.fn)
	defer c.Stop()
	for range 30 {
		c.Push("k", "x", false)
		time.Sleep(2 * time.Millisecond)
	}
	c.Push("k", "y", false)
	if !r.waitFor(1, 400*time.Millisecond) {
		t.Fatalf("timeout; count=%d", r.count())
	}
	// Give the timer a chance to fire any erroneous extras.
	time.Sleep(80 * time.Millisecond)
	if got := r.count(); got != 1 {
		t.Fatalf("want exactly 1 call, got %d", got)
	}
	reason := r.snapshot()[0].reason
	if reason != "x,y" {
		t.Errorf("reason = %q, want %q", reason, "x,y")
	}
}

func TestCoalesce_PerKeyParallel(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: 40 * time.Millisecond, MaxLag: 500 * time.Millisecond}, r.fn)
	defer c.Stop()
	// Chatty key A — keeps pushing for 200ms.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c.Push("A", "a", false)
			}
		}
	}()
	// Quiet key B — one push.
	c.Push("B", "b", false)
	// B should fire well before A finishes; specifically ~Quiet after the push.
	bFired := r.waitFor(1, 200*time.Millisecond)
	if !bFired {
		close(stop)
		wg.Wait()
		t.Fatalf("B did not fire while A was chatty")
	}
	first := r.snapshot()[0]
	if first.key != "B" {
		t.Fatalf("first fired key = %q, want B (A should still be coalescing)", first.key)
	}
	close(stop)
	wg.Wait()
	// Now wait for A to flush.
	if !r.waitFor(2, 400*time.Millisecond) {
		t.Fatalf("A never fired; total count=%d", r.count())
	}
}

func TestCoalesce_MaxLagCap(t *testing.T) {
	r := newRecorder()
	// MaxLag is tight (60ms); Quiet is loose (50ms) so without the cap,
	// continuous events would never let the burst flush.
	c := New(Options{Quiet: 50 * time.Millisecond, MaxLag: 60 * time.Millisecond, SwapHint: 5 * time.Millisecond}, r.fn)
	defer c.Stop()
	stop := time.Now().Add(220 * time.Millisecond)
	for time.Now().Before(stop) {
		c.Push("k", "e", false)
		time.Sleep(5 * time.Millisecond)
	}
	// After we stop pushing, wait for the trailing flush.
	if !r.waitFor(2, 200*time.Millisecond) {
		t.Fatalf("MaxLag cap did not force >=2 flushes during 220ms of churn; got %d", r.count())
	}
	if got := r.count(); got < 2 {
		t.Errorf("want >=2 flushes, got %d", got)
	}
}

func TestCoalesce_MaxLagCapsInitialWindow(t *testing.T) {
	for _, swap := range []bool{false, true} {
		r := newRecorder()
		c := New(Options{Quiet: time.Hour, SwapHint: time.Hour, MaxLag: 10 * time.Millisecond}, r.fn)
		c.Push("k", "first", swap)
		fired := r.waitFor(1, time.Second)
		c.Stop()
		if !fired {
			t.Fatalf("initial window exceeded MaxLag (swap=%v)", swap)
		}
	}
}

func TestCoalesce_MaxLagCapsSwapReset(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: time.Hour, SwapHint: time.Hour, MaxLag: time.Hour}, r.fn)
	defer c.Stop()
	c.Push("k", "first", false)
	c.mu.Lock()
	c.bursts["k"].deadline = time.Now().Add(10 * time.Millisecond)
	c.mu.Unlock()
	c.Push("k", "swap", true)
	if !r.waitFor(1, time.Second) {
		t.Fatal("swap reset extended the burst beyond MaxLag")
	}
}

func TestCoalesce_SwapCommitShortens(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: 100 * time.Millisecond, MaxLag: 500 * time.Millisecond, SwapHint: 10 * time.Millisecond}, r.fn)
	defer c.Stop()
	start := time.Now()
	c.Push("k", "swap", true)
	// Trailing non-swap events — must NOT extend the window past SwapHint.
	for range 3 {
		time.Sleep(2 * time.Millisecond)
		c.Push("k", "chmod", false)
	}
	if !r.waitFor(1, 200*time.Millisecond) {
		t.Fatalf("swap-commit burst did not flush")
	}
	calls := r.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 fire, got %d", len(calls))
	}
	elapsed := calls[0].at.Sub(start)
	// Should be much less than Quiet (100ms). Allow generous CI slack.
	if elapsed > 80*time.Millisecond {
		t.Errorf("swap-commit fired too late: %v (want < 80ms)", elapsed)
	}
	if !strings.Contains(calls[0].reason, "swap") {
		t.Errorf("reason = %q, want it to include 'swap'", calls[0].reason)
	}
}

func TestCoalesce_NoSwapWaitsFullQuiet(t *testing.T) {
	// Baseline for the swap-commit shortening test: without swapCommit,
	// the same sequence waits Quiet.
	r := newRecorder()
	c := New(Options{Quiet: 60 * time.Millisecond, MaxLag: 500 * time.Millisecond}, r.fn)
	defer c.Stop()
	start := time.Now()
	c.Push("k", "a", false)
	if !r.waitFor(1, 300*time.Millisecond) {
		t.Fatalf("no fire")
	}
	if d := r.snapshot()[0].at.Sub(start); d < 40*time.Millisecond {
		t.Errorf("non-swap fired too early: %v (want >= ~Quiet)", d)
	}
}

func TestCoalesce_StopDropsPending(t *testing.T) {
	var fired atomic.Int64
	c := New(Options{Quiet: 30 * time.Millisecond, MaxLag: 300 * time.Millisecond}, func(_, _ string) {
		fired.Add(1)
	})
	c.Push("k", "x", false)
	c.Stop()
	time.Sleep(80 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Errorf("want 0 fires after Stop, got %d", got)
	}
	// Subsequent Push is a no-op.
	c.Push("k", "y", false)
	time.Sleep(80 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Errorf("post-Stop Push fired: %d", got)
	}
}

func TestCoalesce_StopIsIdempotent(t *testing.T) {
	c := New(Options{}, func(_, _ string) {})
	c.Push("k", "x", false)
	c.Stop()
	c.Stop() // must not panic
}

func TestCoalesce_JoinUniqueCollapses(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: 30 * time.Millisecond}, r.fn)
	defer c.Stop()
	c.Push("k", "a", false)
	c.Push("k", "a", false)
	c.Push("k", "b", false)
	c.Push("k", "a", false)
	if !r.waitFor(1, 200*time.Millisecond) {
		t.Fatalf("no fire")
	}
	if got := r.snapshot()[0].reason; got != "a,b" {
		t.Errorf("reason = %q, want %q", got, "a,b")
	}
}

func TestCoalesce_ProfileK8sDefaults(t *testing.T) {
	got := ProfileK8s.Apply()
	if got.Quiet != DefaultQuiet || got.MaxLag != DefaultMaxLag || got.SwapHint != DefaultSwapHint {
		t.Errorf("ProfileK8s = %+v, want defaults", got)
	}
}

func TestCoalesce_ProfileLocalDevLooser(t *testing.T) {
	got := ProfileLocalDev.Apply()
	if got.Quiet <= DefaultQuiet || got.MaxLag <= DefaultMaxLag || got.SwapHint <= DefaultSwapHint {
		t.Errorf("ProfileLocalDev not looser than K8s: %+v", got)
	}
}

func TestCoalesce_Options_NormalizeClampsSwapHint(t *testing.T) {
	o := Options{Quiet: 10 * time.Millisecond, SwapHint: 100 * time.Millisecond}.normalize()
	if o.SwapHint > o.Quiet {
		t.Errorf("SwapHint=%v > Quiet=%v after normalize", o.SwapHint, o.Quiet)
	}
}

func TestCoalesce_Options_NormalizeFillsZeros(t *testing.T) {
	o := Options{}.normalize()
	if o.Quiet != DefaultQuiet || o.MaxLag != DefaultMaxLag || o.SwapHint != DefaultSwapHint {
		t.Errorf("zero Options not filled with defaults: %+v", o)
	}
}

func TestPush_ExpiredBurstAndStaleTimer(t *testing.T) {
	r := newRecorder()
	c := New(Options{Quiet: time.Hour, MaxLag: time.Hour}, r.fn)
	defer c.Stop()
	c.Push("key", "old", false)
	c.mu.Lock()
	old := c.bursts["key"]
	old.deadline = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.Push("key", "new", false)
	calls := r.snapshot()
	if len(calls) != 1 || calls[0].reason != "old" {
		t.Fatalf("expired burst=%v; want old reason", calls)
	}
	c.mu.Lock()
	current := c.bursts["key"]
	c.mu.Unlock()
	defer current.timer.Stop()
	c.flush("key", old)
	if r.count() != 1 {
		t.Fatal("stale timer flushed the new burst")
	}
	c.flush("key", current)
	calls = r.snapshot()
	if len(calls) != 2 || calls[1].reason != "new" {
		t.Fatalf("current burst=%v; want new reason", calls)
	}
}
