package fastconf

// Commit runs the pipeline, publishes state and notifies subscribers.

import (
	"context"
	"log/slog"
	"time"
)

// commit publishes a changed typed value. key identifies the watched directory
// for file-triggered reloads and is empty for other callers.
func (m *Manager[T]) commit(ctx context.Context, assembly assemblyResult, reason, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if assembly.fingerprint != ([32]byte{}) && assembly.fingerprint == m.lastInputs {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "fastconf reload skipped: identical inputs", slog.String("reason", reason))
		return nil
	}
	pipeline, hash, err := m.runPipeline(ctx, assembly, reason, false)
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	prev := m.state.Load()
	if prev != nil && prev.Hash() == hash {
		m.lastInputs = assembly.fingerprint
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "fastconf reload skipped: identical hash", slog.String("reason", reason))
		return nil
	}
	m.lastInputs = assembly.fingerprint
	next := m.newCommittedState(pipeline, hash, reason, key)
	m.publishSnapshot(prev, next, reason)
	return nil
}

func (m *Manager[T]) newCommittedState(pipeline *pipelineState[T], hash [32]byte, reason, key string) *State[T] {
	gen := m.gen.Add(1)
	// Cause.At is the snapshot's commit time; rollback stamps its own.
	now := time.Now().UnixNano()
	cause := ReloadCause{
		Reason:    reason,
		At:        now,
		Revisions: collectProviderRevisions(pipeline.sources),
		Tenant:    m.tenant,
		Key:       key,
	}
	return m.newState(pipeline, hash, gen, cause)
}

func (m *Manager[T]) publishSnapshot(prev, next *State[T], reason string) {
	m.state.Store(next)
	if m.history != nil {
		m.historyMu.Lock()
		if prev != nil {
			m.history.Push(prev)
		}
		m.historyMu.Unlock()
	}
	m.opts.Logger.LogAttrs(context.Background(), slog.LevelInfo, "fastconf reload swap", slog.String("reason", reason), slog.Uint64("generation", next.Generation()), slog.Int("layers", next.sourceCount()))
	if m.observed() {
		var prevGen uint64
		if prev != nil {
			prevGen = prev.Generation()
		}
		m.emit(Committed{
			Prev:   prevGen,
			Next:   next.Generation(),
			Cause:  next.Cause(),
			Layers: next.sourceCount(),
			Diff:   lazyDiff(prev, next),
		})
	}
	m.notifySubscribers(prev, next)
}
