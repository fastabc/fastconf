package fastconf

// Sub-tree introspection (Viper Sub / Koanf Cut / AllKeys / AllSettings).
//
// The reload pipeline already has the *T value, but turning it into a
// dotted-key view requires reflection and JSON-encoding. To keep the
// hot path zero-alloc, we materialise the dotted map lazily and cache
// it on State[T] via atomic.Pointer. The first AllKeys/AllSettings/Sub
// call pays the encoding cost; subsequent calls return the cached
// snapshot.

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// stateKeys is the lazily-built dotted-key view of a State[T].
type stateKeys struct {
	flat map[string]any // "database.dsn" -> "postgres://..."
	keys []string       // sorted dotted keys
}

// keysCache stores the lazily-materialised dotted-key view per State.
// Exposed via an unexported field added to State[T] below in state.go,
// but defined here so this file owns the lifecycle.

// Introspect returns the dotted-key / map[string]any introspection
// sub-API. The strongly-typed hot path is state.Value; Introspect is
// reserved for diagnostics, CLI dump, diff tooling, and other places
// where dynamic keys are unavoidable.
//
// The first call materialises the flat view (one json.Marshal + tree
// walk); subsequent calls reuse a cached snapshot.
func (s *State[T]) Introspect() *Introspection {
	if s == nil {
		return nil
	}
	sk := s.lazyKeys()
	return &Introspection{state: stateIface{redactor: s.redactor}, sk: sk}
}

// Introspection is the dotted-key / map[string]any view of a State[T].
// Always obtained via state.Introspect(); never zero-value-constructed.
type Introspection struct {
	state stateIface
	sk    *stateKeys
}

// stateIface decouples Introspection from State[T]'s generic parameter
// (so the Introspection type is itself non-generic). Only the redactor
// from the parent State is needed for Redacted().
type stateIface struct {
	redactor SecretRedactor
}

// Keys returns every dotted leaf path of the underlying *T in
// deterministic (lexicographic) order.
func (i *Introspection) Keys() []string {
	if i == nil || i.sk == nil {
		return nil
	}
	return i.sk.keys
}

// Settings returns the full dotted-key map as a freshly allocated copy;
// callers may mutate it without affecting the snapshot.
func (i *Introspection) Settings() map[string]any {
	if i == nil || i.sk == nil {
		return nil
	}
	out := make(map[string]any, len(i.sk.flat))
	for k, v := range i.sk.flat {
		out[k] = v
	}
	return out
}

// At returns every dotted key strictly underneath path (prefix stripped),
// as a freshly allocated map. The empty string returns the same shape as
// Settings().
//
// Example: Settings = {"a.b":1,"a.c.d":2,"x":3}
//
//	At("a")   -> {"b":1, "c.d":2}
//	At("a.c") -> {"d":2}
//	At("")    -> identical to Settings()
func (i *Introspection) At(path string) map[string]any {
	if i == nil || i.sk == nil {
		return nil
	}
	if path == "" {
		return i.Settings()
	}
	prefix := path + "."
	out := map[string]any{}
	for k, v := range i.sk.flat {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}

// Sub is a strongly-typed subtree accessor: given an extractor from *T
// to *M, it returns the live *M pointer from the current State. The
// returned pointer aliases State.Value and MUST be treated as
// read-only; mutations leak across goroutines and break the atomic-
// pointer invariant.
//
// Sub mirrors fastconf.Subscribe and fastconf.Eval: every "from *T,
// extract M" operation is a package-level generic function. For dynamic
// (map[string]any) subtree access use state.Introspect().At(path).
func Sub[T any, M any](s *State[T], extract func(*T) *M) *M {
	if s == nil || s.Value == nil || extract == nil {
		return nil
	}
	return extract(s.Value)
}

// lazyKeys returns the materialised stateKeys, computing it on first
// access. Concurrent first-access racers may all compute independently;
// the last writer wins via CAS — output is deterministic so no caller
// sees a stale view.
func (s *State[T]) lazyKeys() *stateKeys {
	if sk := s.keys.Load(); sk != nil {
		return sk
	}
	flat := s.materialiseFlat()
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sk := &stateKeys{flat: flat, keys: keys}
	s.keys.CompareAndSwap(nil, sk)
	return s.keys.Load()
}

// materialiseFlat turns *T into a dotted-key flat map by marshalling to
// JSON (so json struct tags govern naming) and recursively flattening.
// Returns an empty map on encode failure rather than panicking.
func (s *State[T]) materialiseFlat() map[string]any {
	if s.Value == nil {
		return map[string]any{}
	}
	buf, err := json.Marshal(s.Value)
	if err != nil {
		return map[string]any{}
	}
	var tree map[string]any
	if err := json.Unmarshal(buf, &tree); err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	flattenTree("", tree, out)
	return out
}

func flattenTree(prefix string, node map[string]any, out map[string]any) {
	for k, v := range node {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch nested := v.(type) {
		case map[string]any:
			if len(nested) == 0 {
				out[full] = v
				continue
			}
			flattenTree(full, nested, out)
		default:
			out[full] = v
		}
	}
}

// keysHolder is the type held by the atomic.Pointer field on State[T].
// We declare a method here so callers needing extension can subclass
// later without breaking pointer typing.
type keysHolder = atomic.Pointer[stateKeys]
