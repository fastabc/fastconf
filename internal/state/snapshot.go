package state

import (
	"context"
	"maps"
	"reflect"
	"slices"

	"github.com/fastabc/fastconf/feature"
	"github.com/fastabc/fastconf/internal/diffreport"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
)

// State is an immutable snapshot of the configuration at a point in time.
type State[T any] struct {
	value      *T
	hash       [32]byte
	loadedAt   int64
	sources    []SourceRef
	generation uint64
	cause      ReloadCause

	origins  *provenance.Index
	features map[string]feature.Rule
	redactor secret.Redactor
	keys     KeysHolder
}

// Value returns the decoded configuration value. Treat the returned
// pointer as read-only; mutation is undefined behavior.
func (s *State[T]) Value() *T {
	if s == nil {
		return nil
	}
	return s.value
}

// Hash returns the deterministic SHA-256 fingerprint of the merged
// configuration tree. Identical hashes guarantee byte-identical configs.
func (s *State[T]) Hash() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.hash
}

// LoadedAt returns the Unix nanosecond timestamp at which the snapshot
// was committed.
func (s *State[T]) LoadedAt() int64 {
	if s == nil {
		return 0
	}
	return s.loadedAt
}

// Sources returns the ordered list of source layers that were merged
// into this snapshot.
func (s *State[T]) Sources() []SourceRef {
	if s == nil {
		return nil
	}
	return slices.Clone(s.sources)
}

// Generation returns a monotonically increasing counter incremented on
// every successful reload.
func (s *State[T]) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}

// Cause returns the reload trigger metadata recorded when this snapshot
// was committed.
func (s *State[T]) Cause() ReloadCause {
	if s == nil {
		return ReloadCause{}
	}
	return s.cause
}

// NewSnapshot stamps the internal-only fields that callers can observe only
// through State methods.
func NewSnapshot[T any](
	value *T,
	hash [32]byte,
	loadedAt int64,
	sources []SourceRef,
	generation uint64,
	origins *provenance.Index,
	cause ReloadCause,
	features map[string]feature.Rule,
	redactor secret.Redactor,
) *State[T] {
	return &State[T]{
		value:      value,
		hash:       hash,
		loadedAt:   loadedAt,
		sources:    sources,
		generation: generation,
		origins:    origins,
		cause:      cause,
		features:   features,
		redactor:   redactor,
	}
}

// Restamp returns a snapshot with the same immutable payload as s but a new
// generation and cause. It is used by rollback, where the value/hash are from a
// retained snapshot but the publication itself is a fresh commit.
func Restamp[T any](s *State[T], generation uint64, cause ReloadCause) *State[T] {
	if s == nil {
		return nil
	}
	ns := &State[T]{
		value:      s.value,
		hash:       s.hash,
		loadedAt:   cause.At,
		sources:    slices.Clone(s.sources),
		generation: generation,
		origins:    s.origins,
		cause:      cause,
		features:   cloneFeatureRules(s.features),
		redactor:   s.redactor,
	}
	// Carry over the memoised dotted-key view (identical immutable value ⇒
	// identical Keys) without copying the atomic holder itself.
	if k := s.keys.Load(); k != nil {
		ns.keys.Store(k)
	}
	return ns
}

func (s *State[T]) Introspect() *Introspection {
	if s == nil {
		return nil
	}
	return NewIntrospection(LazyMaterialise(&s.keys, s.value))
}

// Extract returns the sub-tree of s.value selected by the user-supplied
// extractor. It is nil-safe: when s, s.value, or extract is nil the
// extractor is not invoked and Extract returns nil.
func Extract[T any, M any](s *State[T], extract func(*T) *M) *M {
	if s == nil || s.value == nil || extract == nil {
		return nil
	}
	return extract(s.value)
}

func (s *State[T]) Redacted() map[string]any {
	if s == nil {
		return nil
	}
	r := s.redactor
	if r == nil {
		r = secret.DefaultRedactor
	}
	return s.Redact(r)
}

func (s *State[T]) FeatureRules() map[string]feature.Rule {
	if s == nil {
		return nil
	}
	return cloneFeatureRules(s.features)
}

func cloneFeatureRules(in map[string]feature.Rule) map[string]feature.Rule {
	if in == nil {
		return nil
	}
	out := make(map[string]feature.Rule, len(in))
	for k, r := range in {
		r.Targets = slices.Clone(r.Targets)
		for i := range r.Targets {
			r.Targets[i].When = maps.Clone(r.Targets[i].When)
		}
		r.Rollouts = slices.Clone(r.Rollouts)
		out[k] = r
	}
	return out
}

func (s *State[T]) Origins() *provenance.Index {
	if s == nil {
		return nil
	}
	return s.origins
}

func (s *State[T]) Explain(path string) []provenance.Origin {
	if s == nil {
		return nil
	}
	return s.origins.Explain(path)
}

func (s *State[T]) LookupStrict(path string) ([]provenance.Origin, error) {
	if s == nil || s.origins == nil {
		return nil, fcerr.ErrNoOrigin
	}
	o := s.origins.Explain(path)
	if len(o) == 0 {
		return nil, fcerr.ErrNoOrigin
	}
	return o, nil
}

// Diff returns the structured per-path differences between s and other,
// comparing the json-encoded view of *T (i.e. user struct tags govern
// field names). The two State pointers may safely be nil; a nil snapshot
// is treated as the empty map.
func (s *State[T]) Diff(other *State[T]) []DiffEntry {
	return DiffMaps("", s.tree(nil), other.tree(nil))
}

// Dump serializes the snapshot to the requested format. When redactor
// is non-nil, secret-tagged paths in *T are masked before encoding;
// when nil, the raw merged tree is emitted. See [Dump] (the package
// helper) for details on each format.
func (s *State[T]) Dump(format DumpFormat, redactor secret.Redactor) ([]byte, error) {
	return Dump(s, format, redactor)
}

func (s *State[T]) Redact(redactor secret.Redactor) map[string]any {
	if redactor == nil {
		redactor = secret.DefaultRedactor
	}
	return s.tree(redactor)
}

// tree is the single map[string]any view used by Diff, Redact, and
// Dump. When redactor is nil the raw ValueMap is returned; otherwise
// secret-tag paths are masked before return. Centralizing here
// guarantees the three out-paths see the same key ordering and
// redaction policy.
func (s *State[T]) tree(redactor secret.Redactor) map[string]any {
	if s == nil || s.value == nil {
		return nil
	}
	raw := ValueMap(s.value)
	if redactor == nil {
		return raw
	}
	paths := secret.Paths(reflect.TypeOf(*s.value))
	return secret.Apply(raw, paths, redactor)
}

type DiffReporter = diffreport.Reporter[DiffEvent]

type DiffReporterFunc func(context.Context, DiffEvent) error

func (f DiffReporterFunc) Report(ctx context.Context, ev DiffEvent) error {
	return f(ctx, ev)
}
