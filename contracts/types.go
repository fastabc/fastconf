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
//   - provider.go  — Provider + WatchPathProvider + Resumable + ErrResumeUnsupported
//   - snapshot.go  — Snapshot + SnapshotProvider
//   - generator.go — Generator
//   - types.go     — Codec, Event, RawLayer, Source, Parser, Span, Attr, Priority* constants
package contracts

import (
	"context"
	"time"
)

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

// RawLayer is a self-describing in-memory contributor: the union of
// (name, codec, bytes, optional priority). It is the carrier the
// Generator contract uses to emit synthetic configuration layers (see
// pkg/generator). For dynamic byte-stream contributors that the
// framework polls or watches, use the Source interface instead.
//
// Priority, when non-zero, is offset into the generator merge band (see
// BandGenerator) so multiple emissions from the same Generator can be
// ordered relative to each other. Zero (the default) is treated as
// PriorityGenerator, so the typical "one layer per Generator" case
// requires no field.
//
//	contracts.RawLayer{Name: "inline", Codec: "yaml", Data: []byte("a: 1")}
type RawLayer struct {
	Name     string
	Codec    string
	Data     []byte
	Priority int
}

// Source is a byte-stream configuration contributor — the koanf-style
// counterpart to Provider. Whereas Provider.Load returns a structured
// map[string]any directly (env, cli, KV with one key per setting),
// Source.Read returns raw bytes plus a content-type hint, which the
// framework pairs with a Parser to obtain the map.
//
// Pair a Source with a Parser via the top-level Bind helper, or use
// the WithSource option which does the Bind internally.
//
// Implementations MUST be safe for concurrent use. The framework calls
// Read from the single reload goroutine but third parties may inspect
// Name / Priority concurrently.
type Source interface {
	// Name is used for diagnostics and dedupe; stable across reads.
	Name() string

	// Priority controls merge order during assemble; higher wins. Use
	// the Priority* constants in this package for well-known bands.
	Priority() int

	// Read returns the current bytes plus a content-type hint (".yaml",
	// "application/yaml", "yaml" — all accepted) and an opaque revision
	// string for change detection. Implementations MAY return an empty
	// contentType when extension/header inference is not possible — the
	// caller is then required to bind an explicit Parser.
	Read(ctx context.Context) (data []byte, contentType string, rev string, err error)

	// Watch optionally streams change events. Returning (nil, nil) means
	// "rely on the global file-watcher / external poll". Same shape as
	// Provider.Watch.
	Watch(ctx context.Context) (<-chan Event, error)
}

// Parser turns raw bytes into a map[string]any. Every Parser SHOULD
// also declare which content-types it answers to, so Bind can pick a
// Parser automatically when the Source supplies a content-type hint.
//
// Parser is a strict superset of Codec; the same value satisfies both.
type Parser interface {
	Codec
	// ContentTypes returns the canonical file extensions and / or MIME
	// types this Parser handles ("yaml", ".yaml", ".yml",
	// "application/yaml"). Used by Bind to auto-select a Parser when
	// the caller passes nil and the Source returned a non-empty
	// contentType hint.
	ContentTypes() []string
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
// User-facing priorities (5–60) are what custom Provider implementations
// declare via Provider.Priority(). The framework offsets them into the
// internal SourcePriorityBand ranges (1000–8999) when reporting
// Snapshot().Sources so providers always win over file layers regardless
// of caller-declared value.
//
// User-facing bands:
//
//	.env file defaults     5  (PriorityDotEnv) — lowest; overridden by all built-ins
//	Static / file-like    10  (PriorityStatic)
//	Overlay providers     20  (PriorityOverlay)
//	Remote KV             30  (PriorityKV)
//	Kubernetes            40  (PriorityK8s)
//	Environment           50  (PriorityEnv)
//	Command-line flags    60  (PriorityCLI)
//	Generator             70  (PriorityGenerator) ← highest user band
//
// Internal bands (used by Snapshot().Sources reporting; not for caller
// use):
//
//	1000 + i              file base layer at discovery index i
//	2000 + i              file overlay layer (single-profile path)
//	3000 + i              file overlay layer (multi-axis / extra)
//	7000 + Priority()     generator emitted Sources
//	8000 + Priority()     provider Load() output
//
// Custom providers SHOULD pick a value within an appropriate user band so
// that merge order remains predictable across heterogeneous deployments.
const (
	PriorityDotEnv    = 5
	PriorityStatic    = 10
	PriorityOverlay   = 20
	PriorityKV        = 30
	PriorityK8s       = 40
	PriorityEnv       = 50
	PriorityCLI       = 60
	PriorityGenerator = 70
)

// SourcePriorityBand offsets for the framework-internal layer ranges.
// Provider/Generator priorities declared by the user are stamped into
// these bands when SourceRef.Priority is populated. Keep names aligned
// with the table above and the trimProviderPrefix helper in the root
// state.go.
const (
	BandFileBase      = 1000
	BandFileOverlay   = 2000
	BandExtraOverlay  = 3000
	BandGenerator     = 7000
	BandProvider      = 8000
	// BandOverride is the priority band for one-shot in-process overrides
	// (WithSourceOverride). Values in this band win over all provider layers.
	BandOverride = 9000
)

// Attr is a (key, value) pair used by tracer attribute fan-out. See
// internal/obs.EnrichAttrs for the canonical span-enrichment helper
// that consumes a variadic Attr slice without per-attribute interface
// allocation.
type Attr struct {
	Key   string
	Value any
}
