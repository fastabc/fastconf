package manager

// Commit: runStages → atomic swap → audit / watch / diff fan-out.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/feature"
)

// commit consumes assembled layers, runs the staged pipeline, and on
// success atomically swaps state. The pipeline itself lives in
// pipeline.go; commit() retains only the terminal "publish" duties:
// hash, swap, history, audit, watches.
func (m *M[T]) commit(ctx context.Context, staged []stagedLayer, appendSlices bool, reason string) error {
	return m.commitWithKey(ctx, staged, appendSlices, reason, "")
}

// commitWithKey is the variant used by the file-system watcher, which
// supplies a parent-directory key so audit fan-out can attribute the
// reload to the specific watched dir whose burst triggered it.
func (m *M[T]) commitWithKey(ctx context.Context, staged []stagedLayer, appendSlices bool, reason, key string) error {
	pc := &pipelineCtx[T]{
		reason:       reason,
		staged:       staged,
		appendSlices: appendSlices,
	}
	if err := m.runStages(ctx, pc); err != nil {
		return err
	}

	// Short-circuit duplicate canonicalHash when mergedJSON has not changed
	// since the last commit. The cache is repopulated below after a
	// successful swap so the first reload always pays the marshal cost
	// (cache miss).
	var hash [32]byte
	if pc.mergedJSON != nil {
		mergedSha := sha256.Sum256(pc.mergedJSON)
		if cached := m.hashCache.Load(); cached != nil && cached.mergedSha == mergedSha {
			hash = cached.stateHash
		} else {
			h, err := canonicalHashBytes(pc.mergedJSON, pc.target, m.opts.CodecBridge)
			if err != nil {
				return fmt.Errorf("fastconf: hash: %w", err)
			}
			hash = h
			m.hashCache.Store(&hashCacheEntry{mergedSha: mergedSha, stateHash: hash})
		}
	} else {
		h, err := canonicalHashBytes(pc.mergedJSON, pc.target, m.opts.CodecBridge)
		if err != nil {
			return fmt.Errorf("fastconf: hash: %w", err)
		}
		hash = h
	}

	prev := m.state.Load()
	if prev != nil && prev.Hash == hash {
		m.opts.Log.Debug().Str("reason", reason).Msg("fastconf reload skipped: identical hash")
		return nil
	}
	gen := m.gen.Add(1)
	cause := istate.ReloadCause{
		Reason:    reason,
		At:        time.Now().UnixNano(),
		Revisions: collectRevisions(pc.sources),
		Tenant:    m.tenant,
		Key:       key,
	}
	features := map[string]feature.Rule(nil)
	if m.opts.FeatureExtract != nil {
		features = m.opts.FeatureExtract(pc.target)
	}
	ns := istate.NewSnapshot(pc.target, hash, time.Now().UnixNano(), pc.sources, gen, pc.origins, cause, features, m.opts.SecretRedactor)
	m.state.Store(ns)
	if m.history != nil {
		m.historyMu.Lock()
		if prev != nil {
			m.history.Push(prev)
		}
		m.historyMu.Unlock()
	}
	m.opts.Metrics.StateGeneration(gen)
	m.opts.Metrics.LayersTotal(len(pc.sources))
	m.opts.Log.Info().
		Str("reason", reason).
		Uint64("generation", gen).
		Int("layers", len(pc.sources)).
		Msg("fastconf reload swap")
	for _, sink := range m.opts.AuditSinks {
		if err := sink.Audit(context.Background(), cause); err != nil {
			m.opts.Log.Warn().Str("reason", reason).Err(err).Msg("fastconf audit sink error")
		}
	}
	m.fireWatches(prev, ns)
	m.fireDiffReporters(prev, ns)
	return nil
}
