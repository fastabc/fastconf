package state

import (
	"context"
	"reflect"

	"github.com/fastabc/fastconf/internal/diffreport"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/pkg/feature"
)

// State is an immutable snapshot of the configuration at a point in time.
type State[T any] struct {
	Value      *T
	Hash       [32]byte
	LoadedAt   int64
	Sources    []SourceRef
	Generation uint64
	Cause      ReloadCause

	origins  *provenance.Index
	features map[string]feature.Rule
	redactor secret.Redactor
	keys     KeysHolder
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
		Value:      value,
		Hash:       hash,
		LoadedAt:   loadedAt,
		Sources:    sources,
		Generation: generation,
		origins:    origins,
		Cause:      cause,
		features:   features,
		redactor:   redactor,
	}
}

func (s *State[T]) Introspect() *Introspection {
	if s == nil {
		return nil
	}
	return NewIntrospection(LazyMaterialise(&s.keys, s.Value))
}

// Extract returns the sub-tree of s.Value selected by the user-supplied
// extractor. It is nil-safe: when s, s.Value, or extract is nil the
// extractor is not invoked and Extract returns nil.
func Extract[T any, M any](s *State[T], extract func(*T) *M) *M {
	if s == nil || s.Value == nil || extract == nil {
		return nil
	}
	return extract(s.Value)
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
	return s.features
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

func (s *State[T]) Lookup(path string) []provenance.Origin {
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
	if s == nil || s.Value == nil {
		return nil
	}
	raw := ValueMap(s.Value)
	if redactor == nil {
		return raw
	}
	paths := secret.Paths(reflect.TypeOf(*s.Value))
	return secret.Apply(raw, paths, redactor)
}

type DiffReporter = diffreport.Reporter[DiffEvent]

type DiffReporterFunc func(context.Context, DiffEvent) error

func (f DiffReporterFunc) Report(ctx context.Context, ev DiffEvent) error {
	return f(ctx, ev)
}
