package fastconf

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/obs"
)

// Observer receives the manager's lifecycle events: metrics, audit logs
// and change notifications are all observers. Reload, stage and Committed
// events are delivered synchronously on the reload goroutine, so a slow
// observer delays the next reload; wrap slow observers with observe.Async.
// ProviderError and EventDropped come from provider watcher goroutines, so
// implementations must be safe for concurrent use. ctx is canceled at
// WithObserverTimeout or on shutdown.
type Observer interface {
	Observe(ctx context.Context, e Event)
}

// Event is one of ReloadStarted, ReloadFinished, StageFinished,
// ProviderError, EventDropped or Committed. Switch on the concrete type
// and ignore the rest; new event types may be added.
type Event interface{ event() }

// ReloadStarted is emitted when a reload begins.
type ReloadStarted struct{ Reason string }

// ReloadFinished is emitted when a reload ends. Err is nil on success,
// including reloads whose result equals the current snapshot.
type ReloadFinished struct {
	Reason string
	Dur    time.Duration
	Err    error
}

// StageFinished reports one pipeline stage: assemble, merge, transform,
// secret, typed-hooks, decode, field-meta, validate, policy or commit.
// Plan dry-runs report their pipeline stages too.
type StageFinished struct {
	Stage string
	Dur   time.Duration
	Err   error
}

// ProviderError reports a provider Watch failure or a resume gap.
type ProviderError struct {
	Provider string
	Err      error
}

// EventDropped reports a provider change event dropped because the reload
// queue was full.
type EventDropped struct{ Source string }

// Committed is emitted after a new snapshot is published. Diff computes the
// redacted per-path changes from Prev to Next on first call and caches
// them; observers that do not need them pay nothing. Layers is the number
// of merged layers.
type Committed struct {
	Prev, Next uint64
	Cause      ReloadCause
	Layers     int
	Diff       func() []DiffEntry
}

func (ReloadStarted) event()  {}
func (ReloadFinished) event() {}
func (StageFinished) event()  {}
func (ProviderError) event()  {}
func (EventDropped) event()   {}
func (Committed) event()      {}

// WithObserver registers observers; events reach them in registration
// order. nil entries fail construction.
func WithObserver(os ...Observer) Option {
	return func(o *options) {
		for i, ob := range os {
			if ob == nil {
				o.DeferredErrs = append(o.DeferredErrs,
					fmt.Errorf("%w: WithObserver: nil observer at #%d", fcerr.ErrFastConf, i))
				continue
			}
			o.Observers = append(o.Observers, ob)
		}
	}
}

// WithObserverTimeout bounds each Observe call (default
// DefaultObserverTimeout); a negative value disables the deadline. The
// deadline only cancels ctx: an observer that ignores ctx still blocks.
func WithObserverTimeout(d time.Duration) Option {
	return func(o *options) { o.ObserverTimeout = d }
}

// observed reports whether any observer is registered. Call sites check it
// before building an event so a manager without observers never boxes one.
func (m *Manager[T]) observed() bool { return len(m.opts.Observers) > 0 }

// emitStage and emitReload are the hot-path emitters; they build nothing
// when no observer is registered.
func (m *Manager[T]) emitStage(stage string, d time.Duration, err error) {
	if m.observed() {
		m.emit(StageFinished{Stage: stage, Dur: d, Err: err})
	}
}

func (m *Manager[T]) emitReload(reason string, d time.Duration, err error) {
	if m.observed() {
		m.emit(ReloadFinished{Reason: reason, Dur: d, Err: err})
	}
}

// emit delivers e to every observer on the calling goroutine.
func (m *Manager[T]) emit(e Event) {
	for _, ob := range m.opts.Observers {
		ctx, cancel := obs.CallbackContext(m.lifetime, m.opts.ObserverTimeout)
		ob.Observe(ctx, e)
		cancel()
	}
}

// lazyDiff memoizes the diagnostic diff for Committed.Diff.
func lazyDiff[T any](prev, next *State[T]) func() []DiffEntry {
	var once sync.Once
	var diff []DiffEntry
	return func() []DiffEntry {
		once.Do(func() {
			if prev != nil {
				diff = diagnosticDiff(prev, next)
			}
		})
		return diff
	}
}

// timed completes the span and stage event even when a stage returns an error.
func (m *Manager[T]) timed(ctx context.Context, spanName string, fn func(context.Context, Span) error) (time.Duration, error) {
	start := time.Now()
	ctx, sp := m.startSpan(ctx, spanName)
	err := fn(ctx, sp)
	elapsed := time.Since(start)
	if err != nil {
		sp.RecordError(err)
	}
	sp.SetAttribute("fastconf.stage.elapsed_ms", elapsed.Milliseconds())
	sp.SetAttribute("fastconf.stage.success", err == nil)
	sp.End()
	m.emitStage(strings.TrimPrefix(spanName, "fastconf."), elapsed, err)
	return elapsed, err
}
