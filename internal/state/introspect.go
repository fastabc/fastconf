package state

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// Keys is the lazily-built dotted-key view of a State[T] value.
type Keys struct {
	Flat   map[string]any // "database.dsn" -> "postgres://..."
	Sorted []string       // sorted dotted keys
}

// KeysHolder is the atomic-pointer holder used to memoise Keys on a
// State[T] value. State[T].keys is of this type; the root package owns
// the field and synchronises first-access racers via CompareAndSwap.
type KeysHolder = atomic.Pointer[Keys]

// Materialise turns a *T into a Keys snapshot. The walk uses JSON so
// the user's struct tags govern the dotted names. Returns an empty
// Keys on encode failure rather than panicking.
func Materialise[T any](value *T) *Keys {
	if value == nil {
		return &Keys{Flat: map[string]any{}}
	}
	buf, err := json.Marshal(value)
	if err != nil {
		return &Keys{Flat: map[string]any{}}
	}
	var tree map[string]any
	if err := json.Unmarshal(buf, &tree); err != nil {
		return &Keys{Flat: map[string]any{}}
	}
	flat := map[string]any{}
	flattenTree("", tree, flat)
	sorted := make([]string, 0, len(flat))
	for k := range flat {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	return &Keys{Flat: flat, Sorted: sorted}
}

// LazyMaterialise reads holder; computes via Materialise on first access.
// Concurrent first-access racers may all compute independently; the
// last writer wins via CompareAndSwap — output is deterministic so no
// caller sees a stale view.
func LazyMaterialise[T any](holder *KeysHolder, value *T) *Keys {
	if k := holder.Load(); k != nil {
		return k
	}
	k := Materialise(value)
	holder.CompareAndSwap(nil, k)
	return holder.Load()
}

func flattenTree(prefix string, node, out map[string]any) {
	for k, v := range node {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok && len(nested) > 0 {
			flattenTree(full, nested, out)
			continue
		}
		out[full] = v
	}
}

// Introspection is the dotted-key / map[string]any view of a State[T].
// Always obtained via State.Introspect(); never zero-value-constructed.
type Introspection struct {
	keys *Keys
}

// NewIntrospection wraps a *Keys into an Introspection view.
func NewIntrospection(k *Keys) *Introspection {
	return &Introspection{keys: k}
}

// Keys returns every dotted leaf path of the underlying *T in
// deterministic (lexicographic) order.
func (i *Introspection) Keys() []string {
	if i == nil || i.keys == nil {
		return nil
	}
	return i.keys.Sorted
}

// Settings returns the full dotted-key map as a freshly allocated copy;
// callers may mutate it without affecting the snapshot.
func (i *Introspection) Settings() map[string]any {
	if i == nil || i.keys == nil {
		return nil
	}
	out := make(map[string]any, len(i.keys.Flat))
	for k, v := range i.keys.Flat {
		out[k] = v
	}
	return out
}

// At returns every dotted key strictly underneath path (prefix
// stripped), as a freshly allocated map. The empty string returns the
// same shape as Settings().
func (i *Introspection) At(path string) map[string]any {
	if i == nil || i.keys == nil {
		return nil
	}
	if path == "" {
		return i.Settings()
	}
	prefix := path + "."
	out := map[string]any{}
	for k, v := range i.keys.Flat {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}
