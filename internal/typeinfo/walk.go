package typeinfo

import (
	"reflect"
)

const maxTypeDepth = 256

// Walk visits exported fields and anonymous structs depth-first, dereferencing pointers.
// Untagged embeds share the parent path. Parent fields are visited before their children
// so metadata can apply to the parent and returning false can skip its descendants.
// Collections are visited without descending into elements.
func Walk(t reflect.Type, visit func(path string, index []int, f reflect.StructField) bool) {
	walk(t, "", nil, visit, 0, map[reflect.Type]struct{}{})
}

func walk(t reflect.Type, prefix string, idx []int, visit func(string, []int, reflect.StructField) bool, depth int, stack map[reflect.Type]struct{}) {
	if t == nil || depth > maxTypeDepth {
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
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !f.IsExported() && (!f.Anonymous || ft.Kind() != reflect.Struct) {
			continue
		}
		name := FieldNamesOf(f).Primary
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
		if ft.Kind() == reflect.Struct {
			walk(ft, path, nextIdx, visit, depth+1, stack)
		}
	}
}
