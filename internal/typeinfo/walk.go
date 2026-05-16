// Package typeinfo provides a single, cached reflect.Type walker for
// FastConf's per-T metadata extraction (secret paths, default tags,
// top-level field hashers). It is the canonical home of FieldName(),
// the json/yaml/lower(field) tag resolution previously duplicated in
// core_secret.go and core_watch.go.
package typeinfo

import (
	"reflect"
	"strings"
	"sync"
)

// Visitor receives per-field callbacks during Walk.
type Visitor interface {
	// OnField fires once per exported struct field (including those reached
	// through anonymous embeds), with the dotted JSON path, field index
	// path, and the StructField itself. Return false to skip descending
	// into this field's sub-tree.
	OnField(path string, index []int, f reflect.StructField) bool
	// OnStructEnter fires before descending into a struct type.
	// Return false to skip the entire struct.
	OnStructEnter(path string, t reflect.Type) bool
	// OnStructLeave fires after all fields of a struct have been visited.
	OnStructLeave(path string, t reflect.Type)
}

// WalkFunc adapts a free function into Visitor when only per-field
// callbacks are needed.
type WalkFunc func(path string, idx []int, f reflect.StructField, st *reflect.Type) bool

// OnField implements Visitor.
func (fn WalkFunc) OnField(path string, idx []int, f reflect.StructField) bool {
	if fn == nil {
		return true
	}
	ft := f.Type
	return fn(path, idx, f, &ft)
}

// OnStructEnter implements Visitor.
func (WalkFunc) OnStructEnter(string, reflect.Type) bool { return true }

// OnStructLeave implements Visitor.
func (WalkFunc) OnStructLeave(string, reflect.Type) {}

// FieldName returns the canonical FastConf name for a struct field:
// json tag → yaml tag → lower(name). Anonymous fields with no tag
// return the empty string so callers can flatten the path.
func FieldName(f reflect.StructField) string {
	if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
		if i := strings.IndexByte(tag, ','); i >= 0 {
			tag = tag[:i]
		}
		if tag != "" {
			return tag
		}
	}
	if tag := f.Tag.Get("yaml"); tag != "" && tag != "-" {
		if i := strings.IndexByte(tag, ','); i >= 0 {
			tag = tag[:i]
		}
		if tag != "" {
			return tag
		}
	}
	if f.Anonymous {
		return ""
	}
	return strings.ToLower(f.Name)
}

const maxWalkDepth = 256

// Walk traverses t depth-first, invoking v on every exported field.
// Pointer indirection and anonymous embedding are flattened transparently;
// slice/map element types are descended only if they (after pointer
// elision) are structs.
func Walk(t reflect.Type, v Visitor) {
	walk(t, "", nil, v, 0)
}

func walk(t reflect.Type, prefix string, idx []int, v Visitor, depth int) {
	if depth > maxWalkDepth {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	if !v.OnStructEnter(prefix, t) {
		return
	}
	defer v.OnStructLeave(prefix, t)
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
		if !v.OnField(path, nextIdx, f) {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			walk(ft, path, nextIdx, v, depth+1)
		case reflect.Slice, reflect.Array:
			elem := ft.Elem()
			for elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				walk(elem, path+".[]", nextIdx, v, depth+1)
			}
		case reflect.Map:
			elem := ft.Elem()
			for elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				walk(elem, path+".{}", nextIdx, v, depth+1)
			}
		}
	}
}

// Cache offers a per-Type, per-Visitor-key result cache so callers
// (secretPathsFor, planForType, buildFieldHashers) can each maintain
// their own answer set without re-walking the type on every reload.
type Cache[V any] struct {
	mu sync.Mutex
	m  map[reflect.Type]V
}

// NewCache creates a new Cache.
func NewCache[V any]() *Cache[V] { return &Cache[V]{m: map[reflect.Type]V{}} }

// GetOrCompute returns the cached value for t or computes it via fn.
// fn runs while the cache lock is held, so it must be deterministic and
// must not call GetOrCompute on the same Cache.
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
