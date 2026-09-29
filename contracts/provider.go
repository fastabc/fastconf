package contracts

import (
	"context"
	"time"
)

// Provider contributes one layer above files and generators. Describer orders providers by
// priority; declaration order breaks ties.
type Provider interface {
	// Name is used for diagnostics and Snapshot().Sources. It SHOULD be
	// stable across runs and unique within a Manager.
	Name() string

	// Load returns a one-shot snapshot of this provider's contribution.
	// Snapshot.Map MUST NOT be retained or mutated by the caller; the
	// provider remains the owner.
	Load(ctx context.Context) (Snapshot, error)

	// Watch streams change events. Returning (nil, nil) means "no native
	// change notifications". The channel SHOULD be closed when ctx is
	// canceled.
	//
	// from is the last Event.Revision (or Snapshot.Revision) the framework
	// observed; it is empty on the first subscribe. Providers that can
	// resume deliver every change strictly after from. Providers that
	// cannot honor from subscribe cold and set Gap on the first event so
	// the framework can record the possibly missed changes.
	Watch(ctx context.Context, from string) (<-chan Event, error)
}

// Snapshot is a provider-owned document. Revision is retained for audit and Watch resumption,
// without bypassing reloads. Stale marks unverified freshness, which is logged and included in
// source diagnostics.
type Snapshot struct {
	Map      map[string]any
	Revision string
	Stale    bool
}

// Event is emitted by Provider.Watch when the provider's view changes.
type Event struct {
	// Source identifies the emitter. Usually equals Provider.Name();
	// providers that fan out sub-keys MAY be more specific.
	Source string
	// Reason is a free-form cause: "watch", "poll-diff", "etag-changed".
	// Used for log lines and reload reasons.
	Reason string
	// Revision, when non-empty, is remembered and passed back to Watch as
	// from on the next subscribe.
	Revision string
	// Gap reports that changes between the requested from revision and
	// this event may have been missed (the provider resubscribed cold).
	Gap bool
	// At is the time the change was observed by the provider.
	At time.Time
}

// ProviderInfo is optional, declarative provider metadata.
type ProviderInfo struct {
	// Priority orders providers; higher wins. Use the Priority* constants.
	Priority int
	// WatchPaths lists local files the provider reads. The framework
	// attaches them to its shared filesystem watcher (parent-directory
	// watches observe Kubernetes-style atomic symlink swaps).
	WatchPaths []string
}

// Describer is implemented by providers that publish ProviderInfo. The framework reads it once
// when the provider is registered; providers without it get Priority 0 and no file watches.
type Describer interface {
	Describe() ProviderInfo
}

// Describe returns p's metadata, or the zero ProviderInfo when p does not implement Describer.
func Describe(p Provider) ProviderInfo {
	if d, ok := p.(Describer); ok {
		return d.Describe()
	}
	return ProviderInfo{}
}
