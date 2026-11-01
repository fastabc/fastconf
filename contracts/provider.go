package contracts

import (
	"context"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// Provider supplies a layer above files and generators, ordered by Describe priority then registration.
// Configure it before registration; fluent mutations must not race Load/Watch.
// Describe is read once, so later changes do not update ordering or watch paths.
type Provider interface {
	// Name is used for diagnostics and Snapshot().Sources. It SHOULD be
	// stable across runs and unique within a Manager.
	Name() string

	// Load returns a one-shot snapshot of this provider's contribution.
	// Snapshot.Map MUST NOT be retained or mutated by the caller; the
	// provider remains the owner.
	Load(ctx context.Context) (Snapshot, error)
}

// Watcher supplies native change notifications. Honor cancellation and allow resubscription
// from the last revision after channel closure/error. Load-only file providers can use
// Describer.WatchPaths instead.
type Watcher interface {
	// Watch returns a channel closed on cancellation; (nil, nil) opts out.
	// from is empty initially, then the last non-empty Event.Revision. Resume strictly
	// after it or mark the first cold event Gap when resumption is unsupported.
	Watch(ctx context.Context, from string) (<-chan Event, error)
}

// Snapshot is a provider-owned document. Revision is retained for source and
// commit diagnostics; Watch resumption uses Event.Revision instead. Stale marks
// unverified freshness, which is logged and included in source diagnostics.
type Snapshot struct {
	Map      map[string]any
	Revision string
	Stale    bool
}

// Event is emitted by Watcher.Watch when the provider's view changes.
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

// DefaultMaxBodyBytes is the default post-response size budget used by the built-in document
// readers.
const DefaultMaxBodyBytes int64 = 4 << 20

// ErrTooLarge identifies a response that exceeded its configured body budget. Readers
// consume max+1 bytes so oversized input is rejected before decoding rather than truncated into a
// different configuration.
var ErrTooLarge = fcerr.ErrTooLarge

// Standard provider priorities; higher wins and declaration order breaks ties. Providers override
// file and generator layers, and precede one-shot overrides.
const (
	PriorityDotEnv = 5
	PriorityStatic = 10
	PriorityKV     = 30
	PriorityK8s    = 40
	PriorityEnv    = 50
	PriorityCLI    = 60
)
