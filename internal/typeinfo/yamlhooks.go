package typeinfo

import (
	"reflect"
	"strings"
)

// BridgeYAMLPkg is the YAML library the typed bridge decodes with. Node hooks
// must take its *yaml.Node; the bridge never calls a hook built on another one.
const BridgeYAMLPkg = "go.yaml.in/yaml/v3"

// YAMLNodeHookPkg returns the package of the Node type t (or *t) accepts in
// UnmarshalYAML(*Node) error, or "" when t has no node-based hook.
func YAMLNodeHookPkg(t reflect.Type) string {
	for _, typ := range []reflect.Type{t, reflect.PointerTo(t)} {
		m, ok := typ.MethodByName("UnmarshalYAML")
		if !ok {
			continue
		}
		ft := m.Type // receiver is In(0)
		if ft.NumIn() != 2 || ft.NumOut() != 1 || ft.Out(0) != reflect.TypeFor[error]() {
			continue
		}
		in := ft.In(1)
		if in.Kind() == reflect.Pointer && in.Elem().Kind() == reflect.Struct && in.Elem().Name() == "Node" {
			return in.Elem().PkgPath()
		}
	}
	return ""
}

// ForeignYAMLNodeHook returns the YAML path and Node package of the first type
// reachable from t, through the YAML bridge's fields, whose UnmarshalYAML takes
// a Node from a library other than BridgeYAMLPkg. Collection elements append
// "[]" and map keys append "[key]". ok is false when every node hook uses the
// bridge's library.
func ForeignYAMLNodeHook(t reflect.Type) (path, pkg string, ok bool) {
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type, string) bool
	visit = func(t reflect.Type, at string) bool {
		if t == nil || seen[t] {
			return false
		}
		seen[t] = true
		if p := YAMLNodeHookPkg(t); p != "" {
			if p == BridgeYAMLPkg {
				return false // the hook decodes this subtree itself
			}
			path, pkg = at, p
			return true
		}
		switch t.Kind() {
		case reflect.Pointer:
			return visit(t.Elem(), at)
		case reflect.Slice, reflect.Array:
			return visit(t.Elem(), at+"[]")
		case reflect.Map:
			return visit(t.Key(), at+"[key]") || visit(t.Elem(), at+"[]")
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				tag := f.Tag.Get("yaml")
				if (!f.IsExported() && !f.Anonymous) || tag == "-" {
					continue
				}
				name := stripTag(tag)
				if name == "" {
					name = strings.ToLower(f.Name)
				}
				if at != "" {
					name = at + "." + name
				}
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				// An inline struct without a node hook contributes its
				// fields directly to the enclosing mapping.
				if YAMLInline(tag) && ft.Kind() == reflect.Struct && YAMLNodeHookPkg(ft) == "" {
					name = at
				}
				if visit(ft, name) {
					return true
				}
			}
		}
		return false
	}
	ok = visit(t, "")
	return path, pkg, ok
}
