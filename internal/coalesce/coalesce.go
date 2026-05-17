// Package coalesce collapses bursty fsnotify events into a single
// trigger per (key, burst) — where "key" is typically the parent
// directory of the events.
//
// Compared to a global time-debouncer, the coalescer
//
//   - parallelises across keys (no head-of-line blocking when one watched
//     directory is chatty),
//   - supports a per-event fast-path signal (swapCommit) that drains the
//     burst on a much shorter window than the quiet default, matching the
//     Kubernetes ConfigMap "..data" atomic-swap pattern,
//   - caps every burst's total lifetime at MaxLag so pathological churn
//     cannot starve the reload pipeline.
//
// The package depends only on the standard library; this rule is checked
// by tools/check-deps.sh and mirrors the constraint pkg/flog operates under.
package coalesce

import (
	"strings"
	"sync"
	"time"
)

// Defaults for Options. Exported so callers (defaults.go) can reference
// the canonical values without duplicating the numbers.
const (
	DefaultQuiet    = 30 * time.Millisecond
	DefaultMaxLag   = 250 * time.Millisecond
	DefaultSwapHint = 5 * time.Millisecond
)

// Options configures a Coalescer. Zero values fall back to the
// Default* constants in this package.
type Options struct {
	// Quiet is the silent-window after which a burst with no further
	// events fires. Default: DefaultQuiet.
	Quiet time.Duration
	// MaxLag is the hard upper bound on a burst's lifetime. Once
	// exceeded the burst is force-flushed even if new events keep
	// arriving. Default: DefaultMaxLag.
	MaxLag time.Duration
	// SwapHint is the (much shorter) quiet window applied once a
	// caller-provided swap-commit event is observed. Default:
	// DefaultSwapHint. Clamped to <= Quiet.
	SwapHint time.Duration
}

func (o Options) normalize() Options {
	if o.Quiet <= 0 {
		o.Quiet = DefaultQuiet
	}
	if o.MaxLag <= 0 {
		o.MaxLag = DefaultMaxLag
	}
	if o.SwapHint <= 0 {
		o.SwapHint = DefaultSwapHint
	}
	// SwapHint must not exceed Quiet — that would invert the contract
	// (a swap should fire sooner than a normal event burst, never later).
	if o.SwapHint > o.Quiet {
		o.SwapHint = o.Quiet
	}
	return o
}

// Coalescer is safe for concurrent Push calls; each key gets its own
// independent burst with its own timer.
type Coalescer struct {
	opts Options
	fn   func(key, reason string)

	mu      sync.Mutex
	bursts  map[string]*burst
	stopped bool
}

type burst struct {
	timer    *time.Timer
	deadline time.Time
	reasons  []string
	swapSeen bool
}

// New constructs a Coalescer with the given options and flush callback.
// fn is invoked from a goroutine spawned by time.AfterFunc; it MUST NOT
// block the caller of Push and SHOULD treat the callback as latency-
// sensitive (the whole point of the coalescer is to minimise time-to-
// reload after the last meaningful event).
func New(opts Options, fn func(key, reason string)) *Coalescer {
	return &Coalescer{
		opts:   opts.normalize(),
		fn:     fn,
		bursts: map[string]*burst{},
	}
}

// Push records an event and (re)arms the burst window for key.
//
// When swapCommit is true the burst's window is tightened to SwapHint
// and subsequent non-swap events in the same burst will not extend it
// — modelling the K8s ConfigMap pattern where a "..data" rename signals
// commit and any trailing CHMOD events are noise.
//
// Push is safe for concurrent use and never blocks on fn.
func (c *Coalescer) Push(key, reason string, swapCommit bool) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}

	now := time.Now()
	var (
		evictedReasons []string
		fireEvicted    bool
	)

	b, ok := c.bursts[key]
	if ok && !now.Before(b.deadline) {
		// MaxLag exceeded: flush the existing burst now and start fresh
		// with this event as the seed for a new burst.
		if b.timer != nil {
			b.timer.Stop()
		}
		evictedReasons = b.reasons
		fireEvicted = len(evictedReasons) > 0
		delete(c.bursts, key)
		ok = false
	}

	if !ok {
		nb := &burst{
			deadline: now.Add(c.opts.MaxLag),
			reasons:  []string{reason},
			swapSeen: swapCommit,
		}
		c.bursts[key] = nb
		window := c.opts.Quiet
		if swapCommit {
			window = c.opts.SwapHint
		}
		k := key
		nb.timer = time.AfterFunc(window, func() { c.flush(k) })
	} else {
		b.reasons = append(b.reasons, reason)
		switch {
		case swapCommit:
			b.swapSeen = true
			b.timer.Reset(c.opts.SwapHint)
		case b.swapSeen:
			// Swap window already running; do not extend.
		default:
			window := min(c.opts.Quiet, time.Until(b.deadline))
			if window > 0 {
				b.timer.Reset(window)
			}
			// window <= 0 is impossible here given the !Before(deadline)
			// check above evicted such bursts already.
		}
	}

	c.mu.Unlock()

	if fireEvicted {
		c.fn(key, joinUnique(evictedReasons))
	}
}

// flush is the timer callback; it acquires the lock, removes the burst,
// and dispatches fn with the collected reasons.
func (c *Coalescer) flush(key string) {
	c.mu.Lock()
	b, ok := c.bursts[key]
	if !ok || c.stopped {
		c.mu.Unlock()
		return
	}
	delete(c.bursts, key)
	reasons := b.reasons
	c.mu.Unlock()

	if len(reasons) == 0 {
		return
	}
	c.fn(key, joinUnique(reasons))
}

// Stop disarms every pending burst and drops their reasons. Subsequent
// Push calls are no-ops. Stop is idempotent.
func (c *Coalescer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	for k, b := range c.bursts {
		if b.timer != nil {
			b.timer.Stop()
		}
		delete(c.bursts, k)
	}
}

// joinUnique returns the input reasons joined by ',' with duplicates
// collapsed. Order is preserved (first-seen wins) so the joined string
// remains a useful audit/log breadcrumb.
func joinUnique(in []string) string {
	if len(in) <= 1 {
		return strings.Join(in, ",")
	}
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
