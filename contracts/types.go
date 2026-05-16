// Package contracts is the **public, stable** surface of FastConf interfaces.
//
// Third-party packages implement these interfaces to extend FastConf with
// custom configuration sources (Vault, Consul, Etcd, AWS AppConfig, ...) and
// custom encodings (TOML, HCL, JSON5, ...). The interfaces are intentionally
// minimal so that v0.x → v1 migration is cheap.
//
// The pipeline packages (fastconf/pkg/provider, fastconf/pkg/decoder, ...)
// type-alias these interfaces so the same value satisfies both the internal
// and the public contract — no adapter shim required.
//
// # Stability
//
// Anything in this package follows semver. Breaking changes require a major
// version bump. Anything outside this package (and the top-level fastconf
// package) is implementation detail.
//
// # Files
//
//   - provider.go  — Provider + Resumable + ErrResumeUnsupported
//   - snapshot.go  — Snapshot + SnapshotProvider
//   - event.go     — Event
//   - codec.go     — Codec
//   - source.go    — Source
//   - priority.go  — PriorityStatic .. PriorityCLI constants
package contracts

import "time"

// Codec decodes a byte stream of a specific encoding (yaml, json, toml, ...)
// into a generic map[string]any used by the merge stage. Implementations
// MUST be safe for concurrent use; the framework calls Decode from the
// reload goroutine but may invoke a single Codec instance from validation
// or test helpers in parallel.
type Codec interface {
	// Decode parses data into a top-level map. Documents whose root is not
	// an object (e.g. a top-level YAML sequence) MUST return an error
	// rather than wrapping the value in a synthetic key.
	Decode(data []byte) (map[string]any, error)
}

// Event is emitted by a Provider when its underlying source changes.
type Event struct {
	// Source identifies which provider/source emitted the event. Usually
	// equals Provider.Name(), but providers that fan out multiple sub-keys
	// MAY use a more specific identifier (e.g. "vault://secret/db").
	Source string
	// Reason is a free-form human readable cause: "watch", "poll-diff",
	// "etag-changed", etc. Used for log lines and metrics labels.
	Reason string
	// Revision, when non-empty, mirrors Snapshot.Revision and lets
	// downstream consumers (e.g. AuditSink) skip duplicate fan-outs.
	Revision string
	// At is the time the change was observed by the provider.
	At time.Time
}

// Source is a self-describing in-memory contributor: the union of
// (name, codec, bytes). It is the lowest-friction way to inject inline
// configuration in tests, examples and bootstrap code without writing a
// full Provider. Typical usage:
//
//	contracts.Source{Name: "inline", Codec: "yaml", Data: []byte("a: 1")}
type Source struct {
	Name  string
	Codec string
	Data  []byte
}

// Span is the minimal tracing-span contract used by FastConf's
// observability layer. Sub-modules (otel/) and the root package both
// reference this type to avoid circular imports between
// internal/obs and the root package.
type Span interface {
	End()
	RecordError(err error)
	SetAttribute(key string, value any)
}

// Standard priority bands. Higher values override lower ones during merge.
//
// File discovery uses 1000-2999 internally; provider bands are kept small
// (10-99) and the framework offsets them above file layers when reporting
// Snapshot().Sources, so providers always win over file layers.
//
//	Static / file-like    10  (PriorityStatic)
//	Overlay providers     20  (PriorityOverlay)
//	Remote KV             30  (PriorityKV)
//	Kubernetes            40  (PriorityK8s)
//	Environment           50  (PriorityEnv)
//	Command-line flags    60  (PriorityCLI)  ← highest
//
// Custom providers SHOULD pick a value within an appropriate band so that
// merge order remains predictable across heterogeneous deployments.
const (
	PriorityDotEnv  = 5  // .env file defaults — lowest band; overridden by all built-in providers
	PriorityStatic  = 10
	PriorityOverlay = 20
	PriorityKV      = 30
	PriorityK8s     = 40
	PriorityEnv     = 50
	PriorityCLI     = 60
)

// Attr is a (key, value) pair used by tracer attribute fan-out.
// (Introduced by BUG-706; see internal/obs.EnrichAttrs.)
type Attr struct {
	Key   string
	Value any
}
