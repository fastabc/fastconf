package fastconf

import (
	"fmt"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/feature"
)

type State[T any] istate.State[T]
type Introspection = istate.Introspection

// DumpFormat selects the encoding used by [State.Dump]. The zero value
// is [DumpYAML].
type DumpFormat = istate.DumpFormat

const (
	// DumpYAML emits deterministic YAML with map keys sorted
	// lexicographically. Two snapshots whose merged values are equal
	// produce byte-identical output, so YAML diffs do not flake.
	DumpYAML = istate.DumpYAML
	// DumpJSON emits indented JSON (two-space indent). Use when piping
	// to jq or another structured-data tool.
	DumpJSON = istate.DumpJSON
	// DumpTOML emits canonical TOML via BurntSushi/toml. Top-level
	// values must be representable as TOML — strings, numbers, bools,
	// tables, and arrays — or the encoder returns an error.
	DumpTOML = istate.DumpTOML
)

// Extract returns the sub-tree of s.Value selected by extract. It is the
// one-shot, type-safe counterpart to [Subscribe]:
//
//   - [Extract] is a synchronous view of the current snapshot.
//   - [Subscribe] streams (oldView, newView) pairs to a callback after
//     every commit and returns a cancel func.
//
// Extract is nil-safe: when any of s, s.Value, or extract is nil it
// returns the zero value of *M (nil) without invoking the extractor.
//
//	dbView := fastconf.Extract(mgr.Snapshot(), func(c *Cfg) *DBSection {
//	    return &c.Database
//	})
func Extract[T any, M any](s *State[T], extract func(*T) *M) *M {
	return istate.Extract(unwrapState(s), extract)
}

// Value returns the decoded configuration value. Treat the returned pointer
// as read-only; mutation is undefined behavior.
func (s *State[T]) Value() *T {
	return unwrapState(s).Value()
}

// Hash returns the deterministic SHA-256 fingerprint of the merged
// configuration tree.
func (s *State[T]) Hash() [32]byte {
	return unwrapState(s).Hash()
}

// LoadedAt returns the Unix nanosecond timestamp at which the snapshot
// was committed.
func (s *State[T]) LoadedAt() int64 {
	return unwrapState(s).LoadedAt()
}

// Sources returns the ordered list of source layers that were merged
// into this snapshot.
func (s *State[T]) Sources() []SourceRef {
	return unwrapState(s).Sources()
}

// Generation returns a monotonically increasing counter incremented on
// every successful reload.
func (s *State[T]) Generation() uint64 {
	return unwrapState(s).Generation()
}

// Cause returns the reload trigger metadata recorded when this snapshot
// was committed.
func (s *State[T]) Cause() ReloadCause {
	return unwrapState(s).Cause()
}

func (s *State[T]) Introspect() *Introspection {
	return unwrapState(s).Introspect()
}

func (s *State[T]) Redacted() map[string]any {
	return unwrapState(s).Redacted()
}

func (s *State[T]) FeatureRules() map[string]feature.Rule {
	return unwrapState(s).FeatureRules()
}

func (s *State[T]) Origins() *provenance.Index {
	return unwrapState(s).Origins()
}

func (s *State[T]) Explain(path string) []provenance.Origin {
	return unwrapState(s).Explain(path)
}

// Lookup returns the provenance chain for path. It is identical to [Explain].
//
// Deprecated: use [Explain] instead. Lookup will be removed in a future version.
func (s *State[T]) Lookup(path string) []provenance.Origin {
	return unwrapState(s).Lookup(path)
}

func (s *State[T]) LookupStrict(path string) ([]provenance.Origin, error) {
	return unwrapState(s).LookupStrict(path)
}

func (s *State[T]) Diff(other *State[T]) []DiffEntry {
	return unwrapState(s).Diff(unwrapState(other))
}

func (s *State[T]) Dump(format DumpFormat, redactor SecretRedactor) ([]byte, error) {
	return unwrapState(s).Dump(format, secret.Redactor(redactor))
}

func (s *State[T]) Redact(redactor SecretRedactor) map[string]any {
	return unwrapState(s).Redact(secret.Redactor(redactor))
}

func wrapState[T any](s *istate.State[T]) *State[T] {
	return (*State[T])(s)
}

func unwrapState[T any](s *State[T]) *istate.State[T] {
	return (*istate.State[T])(s)
}

type (
	SourceRef = istate.SourceRef
	LayerKind = istate.LayerKind
)

const (
	LayerUnknown   = istate.LayerUnknown
	LayerMerge     = istate.LayerMerge
	LayerPatch     = istate.LayerPatch
	LayerProvider  = istate.LayerProvider
	LayerSecret    = istate.LayerSecret
	LayerGenerator = istate.LayerGenerator
	LayerOverride  = istate.LayerOverride
)

type (
	Origin          = provenance.Origin
	OriginIndex     = provenance.Index
	ProvenanceLevel = provenance.Level
)

const (
	ProvenanceOff      = provenance.Off
	ProvenanceTopLevel = provenance.TopLevel
	ProvenanceFull     = provenance.Full
)

type ReloadCause = istate.ReloadCause
type DiffEvent = istate.DiffEvent
type DiffReporter = istate.DiffReporter
type DiffReporterFunc = istate.DiffReporterFunc

// DiffChange classifies one [DiffEntry] as an add, removal, or in-place
// modification. See [State.Diff].
type DiffChange = istate.DiffChange

const (
	DiffAdded    = istate.DiffAdded
	DiffRemoved  = istate.DiffRemoved
	DiffModified = istate.DiffModified
)

// DiffEntry is one structured per-path difference between two State
// snapshots, returned by [State.Diff] and embedded in [DiffEvent] and
// [PlanResult]. Use [FormatDiff] to render a human-readable line list.
type DiffEntry = istate.DiffEntry

// FormatDiff renders a [DiffEntry] sequence as the human-readable line
// list earlier FastConf revisions returned from State.Diff. The output
// format is intended for operator-facing surfaces (logs, fastconfctl,
// PR-bot summaries) and is not covered by the SemVer contract — machine
// consumers should walk [DiffEntry] fields directly.
func FormatDiff(entries []DiffEntry) []string { return istate.FormatDiff(entries) }

func WithDiffReporter(r DiffReporter) Option {
	return func(o *options) {
		if r != nil {
			o.DiffReporters = append(o.DiffReporters, r)
		}
	}
}

func WithDiffReporterQueueCap(n int) Option {
	return func(o *options) {
		if n < 1 {
			n = 1
		}
		o.DiffReporterQueueCap = n
	}
}

// SourcePriorityBand translates a SourceRef's framework-internal
// Priority into a human-readable band label suitable for audit sinks,
// fastconfctl explain, or other operator-facing surfaces.
//
// The returned label has one of these shapes:
//
//	"override"             one-shot in-process override  (9000+)
//	"provider:<name>"      provider                      (8000–8999)
//	"generator:<name>"     generator                     (7000–7999)
//	"file:overlay:<prof>"  single/multi-axis overlay     (2000–6999)
//	"file:base"            base file layer               (1000–1999)
//	"unknown:<n>"          any value not in a known band
func SourcePriorityBand(s SourceRef) string {
	p := s.Priority
	switch {
	case p >= contracts.BandOverride:
		return "override"
	case p >= contracts.BandProvider:
		return "provider:" + trimProviderPrefix(s.Path)
	case p >= contracts.BandGenerator:
		return "generator:" + trimGeneratorPrefix(s.Path)
	case p >= contracts.BandFileOverlay:
		// Covers both single-axis (BandFileOverlay, 2000) and multi-axis
		// (BandExtraOverlay, 3000) overlay layers — they render the same.
		return "file:overlay:" + s.Profile
	case p >= contracts.BandFileBase:
		return "file:base"
	default:
		return fmt.Sprintf("unknown:%d", p)
	}
}

// trimProviderPrefix returns the suffix of a "provider://name" path.
func trimProviderPrefix(p string) string {
	return strings.TrimPrefix(p, "provider://")
}

// trimGeneratorPrefix returns the suffix of a "gen://<name>/<sub>" path
// minus the gen:// scheme, leaving "<name>/<sub>".
func trimGeneratorPrefix(p string) string {
	return strings.TrimPrefix(p, "gen://")
}
