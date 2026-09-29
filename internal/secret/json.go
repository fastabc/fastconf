package secret

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// ApplyJSON follows the actual output names, including literal dots in keys.
// Opaque custom marshalers containing secrets are masked as a whole.
func ApplyJSON(tree map[string]any, t reflect.Type, redactor Redactor) map[string]any {
	if redactor == nil {
		redactor = DefaultRedactor
	}
	value := redactJSON(tree, t, "", redactor)
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{"$redacted": value}
}

var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// PathHasSecrets checks provenance against the type, even when the final
// value omits a field or removes a list. Containers with secret descendants
// are masked as a whole because their historical shape can differ.
func PathHasSecrets(t reflect.Type, path string) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if path == "" || t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return hasSecrets(t, map[reflect.Type]bool{})
	}
	switch t.Kind() {
	case reflect.Struct:
		for _, field := range typeinfo.JSONFields(t) {
			var rest string
			matched := false
			for _, name := range typeinfo.ResolveField(field.Field).Candidates {
				if strings.EqualFold(path, name) {
					matched = true
					break
				}
				if len(path) > len(name) && path[len(name)] == '.' && strings.EqualFold(path[:len(name)], name) {
					rest, matched = path[len(name)+1:], true
					break
				}
			}
			if !matched {
				continue
			}
			ft := t
			for _, index := range field.Field.Index {
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				f := ft.Field(index)
				if HasTag(f.Tag.Get(typeinfo.TagKey)) {
					return true
				}
				ft = f.Type
			}
			if PathHasSecrets(ft, rest) {
				return true
			}
		}
	case reflect.Map:
		// Dotted map keys are ambiguous in provenance; mask if any split
		// reaches a secret rather than exposing one plausible interpretation.
		for {
			_, rest, more := strings.Cut(path, ".")
			if PathHasSecrets(t.Elem(), rest) {
				return true
			}
			if !more {
				break
			}
			path = rest
		}
	case reflect.Slice, reflect.Array:
		_, rest, _ := strings.Cut(path, ".")
		return PathHasSecrets(t.Elem(), rest)
	}
	return false
}

func redactJSON(node any, t reflect.Type, path string, redact Redactor) any {
	if node == nil || t == nil {
		return node
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if (t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType)) && hasSecrets(t, map[reflect.Type]bool{}) {
		return redact(path, node)
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		for _, field := range typeinfo.JSONFields(t) {
			value, exists := m[field.Name]
			if !exists {
				continue
			}
			p := joinPath(path, field.Name)
			ft, marked := t, false
			for _, index := range field.Field.Index {
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				f := ft.Field(index)
				marked = marked || HasTag(f.Tag.Get(typeinfo.TagKey))
				ft = f.Type
			}
			if marked {
				m[field.Name] = redact(p, value)
			} else {
				m[field.Name] = redactJSON(value, ft, p, redact)
			}
		}
	case reflect.Slice, reflect.Array:
		if a, ok := node.([]any); ok {
			for i, value := range a {
				a[i] = redactJSON(value, t.Elem(), joinPath(path, strconv.Itoa(i)), redact)
			}
		}
	case reflect.Map:
		if m, ok := node.(map[string]any); ok {
			for k, value := range m {
				m[k] = redactJSON(value, t.Elem(), joinPath(path, k), redact)
			}
		}
	}
	return node
}

// joinPath appends child to the dotted parent path. All three container
// branches above route through it so a root-level map or slice cannot emit a
// leading separator (".db.token"); redactors that match on exact path text
// rely on the same shape at every depth.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func hasSecrets(t reflect.Type, seen map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if HasTag(f.Tag.Get(typeinfo.TagKey)) || hasSecrets(f.Type, seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		return hasSecrets(t.Elem(), seen)
	}
	return false
}
