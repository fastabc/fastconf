// Package typeinfo provides reflection helpers for secret paths, field tags, and field hashes.
// FieldName resolves json/yaml tags consistently.
package typeinfo

import (
	"reflect"
	"strings"
	"sync"
)

// TagKey is FastConf's struct tag key for field metadata.
const TagKey = "fc"

type FieldAliases struct {
	Canonical  string
	Candidates []string
	Flatten    bool
}

// ResolveField returns the canonical FastConf path segment plus every map-key alias accepted by
// pre-decode walkers. Canonical paths use json tag → yaml tag → lower(name), with anonymous
// untagged embeds flattened. Candidates additionally include the exact Go field name for
// compatibility with typed hook input maps.
func ResolveField(f reflect.StructField) FieldAliases {
	jsonName := stripTag(f.Tag.Get("json"))
	yamlName := stripTag(f.Tag.Get("yaml"))
	out := FieldAliases{}
	switch {
	case jsonName != "" && jsonName != "-":
		out.Canonical = jsonName
	case yamlName != "" && yamlName != "-":
		out.Canonical = yamlName
	case f.Anonymous:
		out.Flatten = true
	default:
		out.Canonical = strings.ToLower(f.Name)
	}
	addCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == "-" {
			return
		}
		for _, existing := range out.Candidates {
			if existing == s {
				return
			}
		}
		out.Candidates = append(out.Candidates, s)
	}
	addCandidate(jsonName)
	addCandidate(yamlName)
	addCandidate(strings.ToLower(f.Name))
	addCandidate(f.Name)
	return out
}

// FieldName returns the canonical FastConf name for a struct field.
func FieldName(f reflect.StructField) string {
	return ResolveField(f).Canonical
}

func stripTag(t string) string {
	if i := strings.IndexByte(t, ','); i >= 0 {
		return t[:i]
	}
	return t
}

const maxWalkDepth = 256

// Walk traverses t depth-first, invoking visit on every exported field. Return false from visit to
// skip the field's descendants. Pointer indirection and anonymous embedding are flattened
// transparently. Slice, array, and map fields are visited, but their elements are not.
// Element-level defaults and field metadata are not supported.
func Walk(t reflect.Type, visit func(path string, index []int, f reflect.StructField) bool) {
	walk(t, "", nil, visit, 0, map[reflect.Type]struct{}{})
}

func walk(t reflect.Type, prefix string, idx []int, visit func(string, []int, reflect.StructField) bool, depth int, stack map[reflect.Type]struct{}) {
	if t == nil || depth > maxWalkDepth {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	if _, ok := stack[t]; ok {
		return
	}
	stack[t] = struct{}{}
	defer delete(stack, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := FieldName(f)
		path := name
		if prefix != "" && name != "" {
			path = prefix + "." + name
		}
		if name == "" {
			path = prefix
		}
		nextIdx := append(append([]int(nil), idx...), i)
		if !visit(path, nextIdx, f) {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			walk(ft, path, nextIdx, visit, depth+1, stack)
		}
	}
}

// Cache stores one result per type so callers can reuse extracted metadata without walking the
// type on every reload.
type Cache[V any] struct {
	mu sync.Mutex
	m  map[reflect.Type]V
}

// NewCache creates a new Cache.
func NewCache[V any]() *Cache[V] { return &Cache[V]{m: map[reflect.Type]V{}} }

// GetOrCompute returns the cached value for t or computes it via fn. fn runs while the cache lock
// is held, so it must be deterministic and must not call GetOrCompute on the same Cache.
func (c *Cache[V]) GetOrCompute(t reflect.Type, fn func() V) V {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[t]; ok {
		return v
	}
	v := fn()
	c.m[t] = v
	return v
}

// fieldAt follows a metadata index without allocating optional parent objects. The final pointer
// is returned intact so required can inspect its nil value.
func fieldAt(value reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return reflect.Value{}
		}
		value = value.Field(i)
	}
	return value
}
