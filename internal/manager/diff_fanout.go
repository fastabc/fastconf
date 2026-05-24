package manager

import (
	"time"

	"github.com/fastabc/fastconf/internal/diffreport"
	iopts "github.com/fastabc/fastconf/internal/options"
	istate "github.com/fastabc/fastconf/internal/state"
)

func (m *M[T]) startDiffReporterWorkers() {
	if len(m.opts.DiffReporters) == 0 {
		return
	}
	qcap := m.opts.DiffReporterQueueCap
	if qcap <= 0 {
		qcap = iopts.DiffReporterQueueCap
	}
	reps := make([]diffreport.Reporter[istate.DiffEvent], 0, len(m.opts.DiffReporters))
	for _, r := range m.opts.DiffReporters {
		reps = append(reps, r)
	}
	m.diffReportPool = diffreport.New[istate.DiffEvent](
		reps, qcap, m.opts.Log.Slog(), m.opts.Metrics,
		&m.bgWG, m.closed,
	)
}

func (m *M[T]) fireDiffReporters(prev, ns *istate.State[T]) {
	if m.diffReportPool == nil || m.diffReportPool.Workers() == 0 || prev == nil {
		return
	}
	diff := prev.Diff(ns)
	if len(diff) == 0 {
		return
	}
	m.diffReportPool.Enqueue(istate.DiffEvent{
		Reason:         ns.Cause().Reason,
		PrevGeneration: prev.Generation(),
		NewGeneration:  ns.Generation(),
		At:             time.Unix(0, ns.LoadedAt()),
		Diff:           diff,
		Cause:          ns.Cause(),
	})
}
