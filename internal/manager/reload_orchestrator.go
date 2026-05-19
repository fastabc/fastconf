package manager

// Reload orchestration: spans + metrics around assemble + commit.

import (
	"context"
	"time"
)

// collectRevisions, mapLayerKind, canonicalHash live in layers.go / hash.go.

// reload runs the full pipeline. On any failure, state is preserved.
func (m *M[T]) reload(ctx context.Context, reason string) error {
	return m.reloadWithKey(ctx, reason, "")
}

// reloadWithKey is the file-system-watcher variant; key is the parent
// dir whose burst triggered this reload (stamped into istate.ReloadCause).
func (m *M[T]) reloadWithKey(ctx context.Context, reason, key string) error {
	start := time.Now()
	m.opts.Metrics.ReloadStarted()
	m.opts.Log.Debug().Str("reason", reason).Msg("fastconf reload start")

	ctx, root := m.startSpan(ctx, "fastconf.reload")
	root.SetAttribute("reason", reason)
	root.SetAttribute("generation", int64(m.gen.Load()))
	defer root.End()

	asmCtx, asmSp := m.startSpan(ctx, "fastconf.assemble")
	staged, appendSlices, err := m.assemble(asmCtx, "")
	if err != nil {
		asmSp.RecordError(err)
		asmSp.End()
		root.RecordError(err)
		m.opts.Metrics.ReloadFinished(false, time.Since(start))
		m.opts.Metrics.StageDuration("assemble", time.Since(start), false)
		m.opts.Log.Warn().Str("reason", reason).Err(err).Msg("fastconf reload shadow_failed")
		return err
	}
	asmSp.SetAttribute("layers", int64(len(staged)))
	asmSp.End()
	m.opts.Metrics.StageDuration("assemble", time.Since(start), true)

	cmtCtx, cmtSp := m.startSpan(ctx, "fastconf.commit")
	commitStart := time.Now()
	if err := m.commitWithKey(cmtCtx, staged, appendSlices, reason, key); err != nil {
		cmtSp.RecordError(err)
		cmtSp.End()
		root.RecordError(err)
		m.opts.Metrics.ReloadFinished(false, time.Since(start))
		m.opts.Metrics.StageDuration("commit", time.Since(commitStart), false)
		m.opts.Log.Warn().Str("reason", reason).Err(err).Msg("fastconf reload commit_failed")
		return err
	}
	cmtSp.End()
	m.opts.Metrics.StageDuration("commit", time.Since(commitStart), true)
	m.opts.Metrics.ReloadFinished(true, time.Since(start))
	return nil
}
