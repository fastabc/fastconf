// Package diffreport is the bounded-queue worker pool that fans
// post-reload diff events out to user-installed Reporter implementations.
// Root fastconf owns the public DiffReporter interface (a type alias to
// Reporter[DiffEvent]) and the WithDiffReporter Option; this package
// owns scheduling and shutdown so root stays free of worker-pool
// plumbing.
package diffreport

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Reporter is the per-event sink. Root re-exports it as
// fastconf.DiffReporter via type alias.
type Reporter[E any] interface {
	Report(ctx context.Context, ev E) error
}

// MetricsSink decouples this package from root's MetricsSink interface.
// Root's MetricsSink satisfies this structurally.
type MetricsSink interface {
	EventDropped(label string)
	DiffReporterQueueDepth(label string, depth, cap int)
}

// Pool owns one bounded queue + goroutine per reporter. Per-reporter
// isolation prevents one slow reporter from starving another.
type Pool[E any] struct {
	workers []*worker[E]
	log     *slog.Logger
	metrics MetricsSink
	closed  <-chan struct{}
}

type worker[E any] struct {
	r     Reporter[E]
	ch    chan E
	label string
}

// New constructs a Pool with one goroutine per reporter. qcap < 1 is
// clamped to 1. Each worker increments wg via wg.Add(1) and calls
// wg.Done on exit; the caller is responsible for Wait() during
// shutdown. Workers exit when closed fires.
//
// We intentionally do NOT close the per-worker channel during shutdown:
// a reload pipeline can still be running when the caller signals
// shutdown, and closing the channel from a different goroutine would
// race with Enqueue's send. By signalling shutdown only via the closed
// channel, the worker exits cleanly and any in-flight non-blocking
// send becomes a drop-on-full no-op (the buffered channel is still
// valid memory).
func New[E any](
	reporters []Reporter[E], qcap int,
	log *slog.Logger, metrics MetricsSink,
	wg *sync.WaitGroup, closed <-chan struct{},
) *Pool[E] {
	if qcap < 1 {
		qcap = 1
	}
	p := &Pool[E]{log: log, metrics: metrics, closed: closed}
	p.workers = make([]*worker[E], 0, len(reporters))
	for i, r := range reporters {
		w := &worker[E]{
			r:     r,
			ch:    make(chan E, qcap),
			label: fmt.Sprintf("diff-reporter:%d", i),
		}
		p.workers = append(p.workers, w)
		wg.Add(1)
		go p.run(w, wg)
	}
	return p
}

// Workers returns the number of installed reporters; useful for tests
// and for nil-safe checks on the caller side.
func (p *Pool[E]) Workers() int {
	if p == nil {
		return 0
	}
	return len(p.workers)
}

// Enqueue is non-blocking. When a reporter's bounded queue is full the
// event is DROPPED and EventDropped("diff-reporter:N") is reported to
// the metrics sink. Reload throughput is therefore independent of
// reporter latency. Queue depth is sampled after every enqueue
// attempt.
func (p *Pool[E]) Enqueue(ev E) {
	if p == nil {
		return
	}
	for _, w := range p.workers {
		select {
		case w.ch <- ev:
		default:
			if p.metrics != nil {
				p.metrics.EventDropped(w.label)
			}
			if p.log != nil {
				p.log.Warn("fastconf diff reporter queue full; event dropped", "reporter", w.label)
			}
		}
		if p.metrics != nil {
			p.metrics.DiffReporterQueueDepth(w.label, len(w.ch), cap(w.ch))
		}
	}
}

func (p *Pool[E]) run(w *worker[E], wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-p.closed:
			return
		case ev := <-w.ch:
			if err := w.r.Report(context.Background(), ev); err != nil && p.log != nil {
				p.log.Warn("fastconf diff reporter error", "err", err, "reporter", w.label)
			}
		}
	}
}
