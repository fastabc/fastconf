package fastconf

import (
	"crypto/sha256"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"sync/atomic"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
)

// State is an immutable snapshot of the configuration at a point in time.
type State[T any] struct {
	value      *T
	hash       [32]byte
	sources    []SourceRef
	generation uint64
	cause      ReloadCause

	origins  *provenance.Index
	redactor secret.Redactor
	secrets  []string
	raw      atomic.Pointer[cachedTree]
}

type cachedTree struct{ value map[string]any }

// Value returns the decoded configuration value. Treat the returned
// pointer as read-only; mutation is undefined behavior.
func (s *State[T]) Value() *T {
	if s == nil {
		return nil
	}
	return s.value
}

// Hash returns the SHA-256 fingerprint of the final typed value encoded as
// JSON, after decoding and defaults. Undecoded input fields do not affect it.
func (s *State[T]) Hash() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.hash
}

// Sources returns the ordered list of source layers that were merged
// into this snapshot.
func (s *State[T]) Sources() []SourceRef {
	if s == nil {
		return nil
	}
	return slices.Clone(s.sources)
}

// sourceCount returns the layer count without copying the source list.
func (s *State[T]) sourceCount() int { return len(s.sourcesRef()) }

// sourcesRef returns the internal source list. Internal callers MUST treat it
// as read-only; the public Sources accessor continues to return a copy.
func (s *State[T]) sourcesRef() []SourceRef {
	if s == nil {
		return nil
	}
	return s.sources
}

// Generation returns a monotonically increasing counter incremented on
// every published change or rollback. Identical reloads keep the generation.
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
	cause := s.cause
	cause.Revisions = maps.Clone(cause.Revisions)
	return cause
}

// newState builds an immutable snapshot from the completed pipeline.
func (m *Manager[T]) newState(p *pipelineState[T], hash [32]byte, generation uint64, cause ReloadCause) *State[T] {
	return &State[T]{value: p.target, hash: hash, sources: p.sources, generation: generation,
		origins: p.origins, cause: cause, redactor: m.opts.SecretRedactor, secrets: m.opts.SecretPaths}
}

// restampState returns a snapshot with the same immutable payload as s but a new
// generation and cause. It is used by rollback, where the value/hash are from a
// retained snapshot but the publication itself is a fresh commit.
func restampState[T any](s *State[T], generation uint64, cause ReloadCause) *State[T] {
	if s == nil {
		return nil
	}
	next := &State[T]{
		value:      s.value,
		hash:       s.hash,
		sources:    slices.Clone(s.sources),
		generation: generation,
		origins:    s.origins,
		cause:      cause,
		redactor:   s.redactor,
		secrets:    s.secrets,
	}
	next.raw.Store(s.raw.Load())
	return next
}

// Map returns the snapshot as a JSON-shaped tree (json struct tags govern
// field names) with secrets masked: fields tagged fc:"secret" and paths
// matched by WithSecretPaths, displayed through WithRedactor. The tree is a
// fresh copy the caller may modify. Use Unredacted for plaintext.
func (s *State[T]) Map() map[string]any {
	return s.tree(s.displayRedactor())
}

// Dump serializes the redacted view (see Map) as YAML, JSON or TOML with
// deterministic key order, so equal snapshots produce byte-identical output.
func (s *State[T]) Dump(format Format) ([]byte, error) {
	return dumpState(s, format, s.displayRedactor())
}

// Diff returns the per-path differences from s to other. Changes are
// detected on plaintext, so a secret that changed is reported, but the
// Before/After values shown are the redacted ones. Either snapshot may be
// nil (treated as empty).
func (s *State[T]) Diff(other *State[T]) []DiffEntry {
	return diagnosticDiff(s, other)
}

// Explain lists the layers that wrote path, lowest priority first. Origin
// values for secret paths are masked. It returns nil when provenance was
// not enabled with WithProvenance or nothing wrote path.
func (s *State[T]) Explain(path string) []Origin {
	if s == nil {
		return nil
	}
	origins := s.origins.Explain(path)
	r := s.displayRedactor()
	for i := range origins {
		if origins[i].Value != nil && (secret.PathHasSecrets(reflect.TypeFor[T](), path) ||
			secret.PatternsMatchValue(origins[i].Value, path, s.secrets)) {
			origins[i].Value = r(path, origins[i].Value)
		}
	}
	return origins
}

// Unredacted gives explicit access to the plaintext views of s.
func (s *State[T]) Unredacted() Unredacted[T] { return Unredacted[T]{s: s} }

// Unredacted exposes a snapshot's plaintext tree. Every call site that
// reaches it is a deliberate decision to handle secrets.
type Unredacted[T any] struct{ s *State[T] }

// Map returns the snapshot tree without masking secrets.
func (u Unredacted[T]) Map() map[string]any { return u.s.tree(nil) }

// Dump serializes the snapshot without masking secrets.
func (u Unredacted[T]) Dump(format Format) ([]byte, error) { return dumpState(u.s, format, nil) }

func (s *State[T]) displayRedactor() secret.Redactor {
	if s != nil && s.redactor != nil {
		return s.redactor
	}
	return secret.DefaultRedactor
}

// tree is the single map[string]any view behind Map, Dump and Diff. When
// redactor is nil the raw JSON tree is returned; otherwise secret paths
// are passed through redactor. Centralizing here guarantees every view
// sees the same key ordering and redaction policy.
func (s *State[T]) tree(redactor secret.Redactor) map[string]any {
	tree, _ := s.treeChecked(redactor)
	return tree
}

func (s *State[T]) treeChecked(redactor secret.Redactor) (map[string]any, error) {
	raw, err := s.rawTreeChecked()
	if err != nil {
		return nil, err
	}
	// Redactors can mutate containers or retain their arguments. Keep the cache
	// private even when returning an unredacted tree (e.g. public Diff entries).
	raw = confmap.DeepClone(raw)
	if redactor == nil || s == nil || s.value == nil {
		return raw, nil
	}
	tree := secret.ApplyJSON(raw, reflect.TypeOf(*s.value), redactor)
	return secret.ApplyPatterns(tree, s.secrets, redactor), nil
}

// rawTreeChecked returns a shared read-only JSON view of this immutable value.
// Racing first readers may encode independently; only a successful view is
// retained. Encoding errors remain observable and can be retried by callers.
func (s *State[T]) rawTreeChecked() (map[string]any, error) {
	if s == nil || s.value == nil {
		return nil, nil
	}
	if cached := s.raw.Load(); cached != nil {
		return cached.value, nil
	}
	raw, err := valueMapChecked(s.value)
	if err != nil {
		return nil, err
	}
	s.raw.CompareAndSwap(nil, &cachedTree{value: raw})
	return s.raw.Load().value, nil
}

// canonicalHash computes SHA-256 over the JSON encoding of *T.
// encoding/json emits struct fields in declaration order and map keys
// in lexicographic order, giving a stable canonical form for free.
func canonicalHash[T any](v *T) ([32]byte, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		var zero [32]byte
		return zero, err
	}
	return sha256.Sum256(buf), nil
}
