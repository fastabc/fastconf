package fastconf

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/fcerr"
)

// commit publishes a changed typed value or resolved-secret path set. key is the
// watched directory for file-triggered reloads and is empty for other callers.
func (m *Manager[T]) commit(ctx context.Context, assembly assemblyResult, trigger ReloadTrigger, reason, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if assembly.fingerprint != ([32]byte{}) && assembly.fingerprint == m.publishedFingerprint {
		m.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "fastconf reload skipped: identical inputs", slog.String("reason", reason))
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
	// File decoders may register replacements while the deferred decode runs.
	// Never associate such a mixed result with the earlier input generation.
	if assembly.codecGeneration != codec.Generation() {
		assembly.fingerprint = [32]byte{}
	}
	valueChanged := prev == nil || prev.Hash() != hash
	if !valueChanged && slices.EqualFunc(prev.resolvedSecrets, pipeline.resolvedSecrets, slices.Equal[[]string]) {
		m.publishedFingerprint = assembly.fingerprint
		m.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "fastconf reload skipped: identical hash", slog.String("reason", reason))
		return nil
	}
	m.publishedFingerprint = assembly.fingerprint
	next := m.newCommittedState(pipeline, hash, trigger, reason, key)
	m.publishSnapshot(ctx, prev, next, reason)
	if valueChanged {
		m.notifySubscribers(ctx, prev, next)
	}
	return nil
}

func (m *Manager[T]) newCommittedState(pipeline *pipelineState[T], hash [32]byte, trigger ReloadTrigger, reason, key string) *State[T] {
	gen := m.gen.Add(1)
	// Cause.At is the snapshot's commit time; rollback stamps its own.
	now := time.Now()
	cause := ReloadCause{
		Reason:    reason,
		Trigger:   trigger,
		At:        now,
		Revisions: collectProviderRevisions(pipeline.sources),
		Tenant:    m.tenant,
		Key:       key,
	}
	return m.newState(pipeline, hash, gen, cause)
}

func (m *Manager[T]) publishSnapshot(ctx context.Context, prev, next *State[T], reason string) {
	m.state.Store(next)
	if m.history != nil {
		m.historyMu.Lock()
		if prev != nil {
			m.history.Push(prev)
		}
		m.historyMu.Unlock()
	}
	m.opts.Logger.LogAttrs(ctx, slog.LevelInfo, "fastconf reload swap", slog.String("reason", reason), slog.Uint64("generation", next.Generation()), slog.Int("layers", next.sourceCount()))
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
}

// ReloadTrigger is a bounded classification of what started a reload, plan or
// rollback. Unlike ReloadCause.Reason, which can embed file paths, provider
// event text or a WithReason value, it takes only the Trigger* values below
// (new values may be added in later minor releases), so it is safe as a metric
// label or audit dimension.
type ReloadTrigger = fcerr.Trigger

// ReloadTrigger values.
const (
	// TriggerInitial is the first load performed by New or Load.
	TriggerInitial ReloadTrigger = "initial"
	// TriggerManual is Manager.Reload, with or without WithOverride or WithReason.
	TriggerManual ReloadTrigger = "manual"
	// TriggerFile is a coalesced file-watcher event or watcher error.
	TriggerFile ReloadTrigger = "file"
	// TriggerProvider is a provider Watch event, or the compensating refresh
	// after provider events were dropped ("provider:resync").
	TriggerProvider ReloadTrigger = "provider"
	// TriggerNotify is a refresh requested by a WithNotifier consumer.
	TriggerNotify ReloadTrigger = "notify"
	// TriggerResync is the periodic reconciliation configured by WithResync.
	TriggerResync ReloadTrigger = "resync"
	// TriggerRollback is History.Rollback or History.RollbackContext.
	TriggerRollback ReloadTrigger = "rollback"
	// TriggerPlan is Manager.Plan. It appears on PlanResult.Proposed and on
	// Errors entries for failed plans; plans never publish.
	TriggerPlan ReloadTrigger = "plan"
)

// ReloadCause is the audit-friendly explanation of a successful commit.
// It is carried by the Committed observer event and surfaced on State[T].Cause so
// downstream tooling can trace an in-process change back to the event
// (file change, provider push, Reload) that drove it.
type ReloadCause struct {
	// Reason mirrors the request reason: "initial", "load", "plan", "manual",
	// "override", a WithReason value, "rollback", "resync", "provider:resync",
	// "provider:<name>[:<event reason>]", "notify:<name>" or "notify[<i>]",
	// and for file events a comma-joined "fs:<op>:<path>" / "fs-error:<msg>"
	// list. File and provider reasons embed paths or provider-supplied text,
	// so they are unbounded: use Trigger as a metric dimension instead.
	Reason string
	// Trigger classifies Reason into a bounded set of values; see
	// ReloadTrigger. It is empty only for hand-built causes.
	Trigger ReloadTrigger
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
	// dir); empty for watcher errors ("fs-error:..."), manual,
	// provider-driven, initial and rollback commits.
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
