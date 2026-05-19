package state

import "time"

// ReloadCause is the audit-friendly explanation of a successful commit.
// It is emitted to every AuditSink and surfaced on State[T].Cause so
// downstream tooling can trace an in-process change back to the event
// (file change, provider push, Reload) that drove it.
//
// Root fastconf re-exports this as `type ReloadCause = istate.ReloadCause`
// so the public API (cmd/fastconfd, tenant.go, obs.go, ...) is unchanged.
type ReloadCause struct {
	// Reason mirrors the reloadRequest reason ("initial",
	// "provider:vault://...", "manual", "watcher", ...). Stable string
	// safe for log labels and metric dimensions.
	Reason string
	// At is the wall-clock instant the reload pipeline started.
	At int64
	// Revisions captures every provider's reported revision at the time
	// of assemble (provider name -> revision string). Empty for plain
	// file-only configurations.
	Revisions map[string]string
	// Tenant, when non-empty, identifies which logical tenant this
	// commit belongs to. For single-tenant deployments this is always "".
	Tenant string
	// Key, when non-empty, identifies the watched parent directory whose
	// fsnotify event burst triggered this reload. Populated only for
	// file-system driven reloads (the coalescer keys bursts by parent
	// dir); empty for manual, provider-driven, and initial reloads.
	Key string
}

// DiffEvent is the payload handed to every DiffReporter.
//
// Root fastconf re-exports this as `type DiffEvent = istate.DiffEvent`
// so installed reporters compile against the same shape. Lives in
// internal/state because its Cause field references ReloadCause, which
// also lives here.
type DiffEvent struct {
	// Reason mirrors ReloadCause.Reason — "manual", "watcher",
	// "provider:vault://...", "override", etc.
	Reason string
	// PrevGeneration is the generation number of the State that was
	// just replaced; zero on the first reload.
	PrevGeneration uint64
	// NewGeneration is the generation number just published.
	NewGeneration uint64
	// At captures when the reload swap occurred.
	At time.Time
	// Diff is the structured per-path change list produced by State.Diff.
	// Empty when the previous state had a different hash but identical
	// field values (which should be rare once canonicalisation has run).
	// Use FormatDiff to render a human-readable line list.
	Diff []DiffEntry
	// Cause is the full ReloadCause for downstream tooling that needs
	// the audit trail (revisions, tenant, request id, ...).
	Cause ReloadCause
}
