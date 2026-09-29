// Package observe provides composable fastconf.Observer implementations:
// fan-out (Multi), asynchronous delivery (Async), a JSON-lines audit log
// (JSONLines), a metrics-sink adapter (Metrics) and a function adapter
// (Func).
package observe

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastabc/fastconf"
)

// Func adapts a function to fastconf.Observer.
type Func func(ctx context.Context, e fastconf.Event)

// Observe implements fastconf.Observer.
func (f Func) Observe(ctx context.Context, e fastconf.Event) { f(ctx, e) }

// Multi delivers every event to each observer in order; nil entries are
// skipped.
func Multi(os ...fastconf.Observer) fastconf.Observer {
	return Func(func(ctx context.Context, e fastconf.Event) {
		for _, o := range os {
			if o != nil {
				o.Observe(ctx, e)
			}
		}
	})
}

// AsyncObserver delivers events to its target on a dedicated goroutine
// through a bounded queue. Create it with Async and Close it after the
// manager so queued events drain.
type AsyncObserver struct {
	target fastconf.Observer
	ch     chan fastconf.Event
	done   chan struct{}
	// mu orders sends against close: Observe holds it shared, Close
	// exclusively, so no send can reach a closed channel.
	mu      sync.RWMutex
	closed  bool
	dropped atomic.Uint64
}

// Async wraps target so Observe never blocks the reload goroutine. When the
// queue (queueCap, minimum 1) is full the event is dropped and counted in
// Dropped. Committed events carry a lazily computed Diff; calling it from
// the async goroutine is safe.
func Async(target fastconf.Observer, queueCap int) *AsyncObserver {
	if queueCap < 1 {
		queueCap = 1
	}
	a := &AsyncObserver{target: target, ch: make(chan fastconf.Event, queueCap), done: make(chan struct{})}
	go a.run()
	return a
}

func (a *AsyncObserver) run() {
	defer close(a.done)
	for e := range a.ch {
		a.target.Observe(context.Background(), e)
	}
}

// Observe implements fastconf.Observer.
func (a *AsyncObserver) Observe(_ context.Context, e fastconf.Event) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		a.dropped.Add(1)
		return
	}
	select {
	case a.ch <- e:
	default:
		a.dropped.Add(1)
	}
}

// Dropped reports how many events were discarded because the queue was
// full or the observer was closed.
func (a *AsyncObserver) Dropped() uint64 { return a.dropped.Load() }

// Close stops accepting events and waits for queued ones to be delivered.
// Close the manager first; events observed after Close are dropped.
func (a *AsyncObserver) Close() error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		close(a.ch)
	}
	a.mu.Unlock()
	<-a.done
	return nil
}

// MetricsSink is the method set of the prometheus satellite's Sink
// (observability/metrics/prometheus); Metrics adapts any value with it.
type MetricsSink interface {
	ReloadFinished(ok bool, dur time.Duration)
	StateGeneration(gen uint64)
	LayersTotal(n int)
	ProviderError(provider string)
	EventDropped(source string)
	StageDuration(stage string, dur time.Duration, ok bool)
}

// Metrics translates events into sink calls: reload results and stage
// timings, the committed generation and layer count, provider errors and
// dropped events.
func Metrics(sink MetricsSink) fastconf.Observer {
	return Func(func(_ context.Context, e fastconf.Event) {
		switch ev := e.(type) {
		case fastconf.ReloadFinished:
			sink.ReloadFinished(ev.Err == nil, ev.Dur)
		case fastconf.StageFinished:
			sink.StageDuration(ev.Stage, ev.Dur, ev.Err == nil)
		case fastconf.Committed:
			sink.StateGeneration(ev.Next)
			sink.LayersTotal(ev.Layers)
		case fastconf.ProviderError:
			sink.ProviderError(ev.Provider)
		case fastconf.EventDropped:
			sink.EventDropped(ev.Source)
		}
	})
}

// JSONLines writes one JSON object per Committed event (reason, commit
// time, provider revisions, tenant, generations) to w. Writes are
// serialized; w must bound its own blocking.
func JSONLines(w io.Writer) fastconf.Observer {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	return Func(func(_ context.Context, e fastconf.Event) {
		c, ok := e.(fastconf.Committed)
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(struct {
			Reason     string            `json:"reason"`
			At         time.Time         `json:"at"`
			Generation uint64            `json:"generation"`
			Previous   uint64            `json:"previous,omitempty"`
			Revisions  map[string]string `json:"revisions,omitempty"`
			Tenant     string            `json:"tenant,omitempty"`
		}{
			Reason:     c.Cause.Reason,
			At:         time.Unix(0, c.Cause.At),
			Generation: c.Next,
			Previous:   c.Prev,
			Revisions:  c.Cause.Revisions,
			Tenant:     c.Cause.Tenant,
		})
	})
}
