// Package debounce provides a single-writer trailing-edge debouncer used by
// the watcher subsystem to coalesce bursty filesystem events into one reload
// trigger.
package debounce

import (
	"strings"
	"sync"
	"time"
)

// Debouncer collects calls to Trigger inside a sliding window and fires the
// callback exactly once with all merged reasons after the window elapses.
//
// It is safe for concurrent Trigger calls and survives stop/restart.
type Debouncer struct {
	interval time.Duration
	fn       func(reason string)

	mu      sync.Mutex
	timer   *time.Timer
	reasons []string
	stopped bool
}

// New constructs a Debouncer. Use Stop to release resources.
func New(interval time.Duration, fn func(reason string)) *Debouncer {
	if interval <= 0 {
		// Keep in sync with fastconf.DefaultDebounceInterval in fastconf/defaults.go.
		// This package cannot import fastconf (circular dep), so the value is duplicated.
		interval = 500 * time.Millisecond
	}
	return &Debouncer{interval: interval, fn: fn}
}

// Trigger records a reason and (re)arms the window.
func (d *Debouncer) Trigger(reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.reasons = append(d.reasons, reason)
	if d.timer == nil {
		d.timer = time.AfterFunc(d.interval, d.flush)
		return
	}
	d.timer.Reset(d.interval)
}

func (d *Debouncer) flush() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	reasons := d.reasons
	d.reasons = nil
	d.timer = nil
	d.mu.Unlock()

	if len(reasons) == 0 {
		return
	}
	d.fn(joinUnique(reasons))
}

// Stop disarms the timer; pending callbacks are dropped.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func joinUnique(in []string) string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}
