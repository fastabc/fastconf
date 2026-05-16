package contracts

import "context"

// Provider is the contract every dynamic configuration source implements.
//
// A Provider participates in the reload pipeline as a single layer: its
// Load method returns a snapshot map that is merged on top of file-discovery
// layers (and other providers) ordered by Priority — higher Priority wins.
//
// Watch is optional: providers that have no native change-notification
// channel should return (nil, nil), and the framework will treat them as
// polled / static. Returning a non-nil channel makes the provider eligible
// for event-driven reloads in conjunction with Manager's watcher.
type Provider interface {
	// Name is used for diagnostics, deduplication and Snapshot().Sources.
	// It SHOULD be stable across runs and unique within a Manager.
	Name() string

	// Priority controls merge order: higher values override lower ones.
	// Use the Priority* constants in this package for well-known bands.
	Priority() int

	// Load returns a one-shot snapshot of this provider's contribution.
	// The returned map MUST NOT be retained or mutated by the caller; the
	// provider remains the owner.
	Load(ctx context.Context) (map[string]any, error)

	// Watch optionally streams change events. Returning (nil, nil) means
	// "no native change notifications". The returned channel SHOULD be
	// closed when ctx is cancelled.
	Watch(ctx context.Context) (<-chan Event, error)
}

// Resumable is the Phase 25 optional extension. Providers that can
// re-subscribe from a known revision (etcd-style "Watch from
// last_revision", NATS JetStream "DeliverByStartSequence", Vault
// kv-v2 metadata version) implement WatchFrom. The framework
// remembers the last Event.Revision (or Snapshot.Revision) it
// observed for each provider and passes it back on the next
// (re)subscribe. Providers that do not implement Resumable continue
// to call Watch as before, preserving backwards compatibility.
//
// WatchFrom MUST behave like Watch when lastRev is empty (cold
// subscribe). When lastRev is non-empty, the provider SHOULD attempt
// to deliver every change observed strictly AFTER that revision; if
// the upstream cannot honour the request (e.g. revision compacted),
// the provider MUST return ErrResumeUnsupported so the framework
// falls back to Watch and the caller can mark the gap in audit.
type Resumable interface {
	WatchFrom(ctx context.Context, lastRev string) (<-chan Event, error)
}

// ErrResumeUnsupported is returned by Resumable.WatchFrom when the
// requested resume point is no longer recoverable (compaction,
// retention loss). The framework converts this into a metric +
// audit annotation and falls back to a cold Watch.
var ErrResumeUnsupported = errResumeUnsupported{}

type errResumeUnsupported struct{}

func (errResumeUnsupported) Error() string { return "resume unsupported: revision unrecoverable" }
