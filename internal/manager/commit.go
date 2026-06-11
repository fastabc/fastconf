package manager

// Commit: runStages → atomic swap → audit / watch / diff fan-out.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/fastabc/fastconf/feature"
	istate "github.com/fastabc/fastconf/internal/state"
)

// commitWithKey is the variant used by the file-system watcher, which
// supplies a parent-directory key so audit fan-out can attribute the
// reload to the specific watched dir whose burst triggered it.
func (m *M[T]) commitWithKey(ctx context.Context, asm assemblyResult, reason, key string) error {
	pc, err := m.runPipelineForCommit(ctx, asm, reason)
	if err != nil {
		return err
	}
	hash, err := m.stateHashFor(pc)
	if err != nil {
		return err
	}
	prev := m.state.Load()
	if prev != nil && prev.Hash() == hash {
		m.opts.Log.Debug().Str("reason", reason).Msg("fastconf reload skipped: identical hash")
		return nil
	}
	ns := m.buildSnapshotForPublish(pc, hash, reason, key)
	m.publishSnapshot(prev, ns, reason)
	m.fanoutAfterPublish(prev, ns, reason)
	return nil
}

func (m *M[T]) runPipelineForCommit(ctx context.Context, asm assemblyResult, reason string) (*pipelineCtx[T], error) {
	pc := &pipelineCtx[T]{
		reason:       reason,
		staged:       asm.staged,
		appendSlices: asm.appendSlices,
		mergeKeys:    asm.mergeKeys,
	}
	if err := m.runStages(ctx, pc); err != nil {
		return nil, err
	}
	return pc, nil
}

func (m *M[T]) stateHashFor(pc *pipelineCtx[T]) ([32]byte, error) {
	// Short-circuit duplicate canonicalHash when mergedJSON has not changed
	// since the last commit. The cache is repopulated below after a
	// successful swap so the first reload always pays the marshal cost
	// (cache miss).
	var mergedSha [32]byte
	if pc.mergedJSON != nil {
		mergedSha = sha256.Sum256(pc.mergedJSON)
		if cached := m.hashCache.Load(); cached != nil && cached.mergedSha == mergedSha {
			return cached.stateHash, nil
		}
	}
	h, err := canonicalHashBytes(pc.mergedJSON, pc.target, m.opts.CodecBridge)
	if err != nil {
		return [32]byte{}, fmt.Errorf("fastconf: hash: %w", err)
	}
	if pc.mergedJSON != nil {
		m.hashCache.Store(&hashCacheEntry{mergedSha: mergedSha, stateHash: h})
	}
	return h, nil
}

func (m *M[T]) buildSnapshotForPublish(pc *pipelineCtx[T], hash [32]byte, reason, key string) *istate.State[T] {
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
	return istate.NewSnapshot(pc.target, hash, time.Now().UnixNano(), pc.sources, gen, pc.origins, cause, features, m.opts.SecretRedactor)
}

func (m *M[T]) publishSnapshot(prev, ns *istate.State[T], reason string) {
	m.state.Store(ns)
	if m.history != nil {
		m.historyMu.Lock()
		if prev != nil {
			m.history.Push(prev)
		}
		m.historyMu.Unlock()
	}
	m.opts.Metrics.StateGeneration(ns.Generation())
	m.opts.Metrics.LayersTotal(len(ns.Sources()))
	m.opts.Log.Info().
		Str("reason", reason).
		Uint64("generation", ns.Generation()).
		Int("layers", len(ns.Sources())).
		Msg("fastconf reload swap")
	m.refreshWatchPathsFromState(ns)
}

func (m *M[T]) fanoutAfterPublish(prev, ns *istate.State[T], reason string) {
	cause := ns.Cause()
	for _, sink := range m.opts.AuditSinks {
		if err := sink.Audit(context.Background(), cause); err != nil {
			m.opts.Log.Warn().Str("reason", reason).Err(err).Msg("fastconf audit sink error")
		}
	}
	m.fireWatches(prev, ns)
	m.fireDiffReporters(prev, ns)
}
