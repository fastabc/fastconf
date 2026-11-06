package fastconf

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
	if assembly.fingerprint != ([32]byte{}) && assembly.fingerprint == m.publishedFingerprint {
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
		m.publishedFingerprint = assembly.fingerprint
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "fastconf reload skipped: identical hash", slog.String("reason", reason))
		return nil
	}
	m.publishedFingerprint = assembly.fingerprint
	next := m.newCommittedState(pipeline, hash, reason, key)
	m.publishSnapshot(prev, next, reason)
	return nil
}

func (m *Manager[T]) newCommittedState(pipeline *pipelineState[T], hash [32]byte, reason, key string) *State[T] {
	gen := m.gen.Add(1)
	// Cause.At is the snapshot's commit time; rollback stamps its own.
	now := time.Now()
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
	if m.hasObservers() {
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

// ReloadCause is the audit-friendly explanation of a successful commit.
// It is carried by the Committed observer event and surfaced on State[T].Cause so
// downstream tooling can trace an in-process change back to the event
// (file change, provider push, Reload) that drove it.
type ReloadCause struct {
	// Reason mirrors the reloadRequest reason ("initial",
	// "provider:vault://...", "manual", "watcher", ...). Stable string
	// safe for log labels and metric dimensions.
	Reason string
	// At is the snapshot commit time, or the preview time for Plan.
	At time.Time
	// Revisions captures every provider's reported revision at the time
	// of assemble (provider name -> revision string). Empty for plain
	// file-only configurations.
	Revisions map[string]string
	// Tenant is the WithTenant id; empty unless the manager was tagged.
	Tenant string
	// Key, when non-empty, identifies the watched parent directory whose
	// fsnotify event burst triggered this reload. Populated only for
	// file-system driven reloads (the coalescer keys bursts by parent
	// dir); empty for manual, provider-driven, and initial reloads.
	Key string
}

// collectProviderRevisions extracts the per-provider revision map from sources;
// only LayerProvider entries with non-empty Revision are included.
func collectProviderRevisions(sources []SourceRef) map[string]string {
	var out map[string]string
	for _, s := range sources {
		if s.Kind != LayerProvider || s.Revision == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		// Strip the "provider://" prefix for readability.
		name := s.Path
		if len(name) > len(providerPathPrefix) && name[:len(providerPathPrefix)] == providerPathPrefix {
			name = name[len(providerPathPrefix):]
		}
		out[name] = s.Revision
	}
	return out
}

const providerPathPrefix = "provider://"
