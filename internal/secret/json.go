package secret

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// Plan contains immutable type metadata and path rules. It is safe to share
// between snapshots and concurrent readers; no configuration values are cached.
type Plan struct {
	root     *jsonPlan
	patterns patternPlan
}

type jsonField struct {
	name       string
	candidates []string
	marked     bool
	child      *jsonPlan
	inline     bool
}

type jsonPlan struct {
	kind            reflect.Kind
	opaque, secrets bool
	fields          []jsonField
	origins         []jsonField
	elem            *jsonPlan
}

// Compile builds metadata once, including cycles in recursive types.
func Compile(t reflect.Type, patterns []string) *Plan {
	memo := map[reflect.Type]*jsonPlan{}
	var build func(reflect.Type) *jsonPlan
	build = func(t reflect.Type) *jsonPlan {
		if t == nil {
			return nil
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if p := memo[t]; p != nil {
			return p
		}
		p := &jsonPlan{kind: t.Kind(), opaque: opaqueJSONType(t), secrets: hasSecrets(t, map[reflect.Type]bool{})}
		memo[t] = p
		if !p.secrets || p.opaque {
			return p
		}
		switch t.Kind() {
		case reflect.Struct:
			for _, field := range typeinfo.JSONFields(t) {
				ft, marked := t, false
				for _, index := range field.Field.Index {
					for ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					f := ft.Field(index)
					marked = marked || HasTag(f.Tag.Get(typeinfo.TagKey))
					ft = f.Type
				}
				p.fields = append(p.fields, jsonField{name: field.Name, marked: marked, child: build(ft)})
			}
			p.origins = provenanceFields(t, false, map[reflect.Type]bool{}, build)
		case reflect.Map, reflect.Array, reflect.Slice:
			p.elem = build(t.Elem())
		}
		return p
	}
	return &Plan{root: build(t), patterns: compilePatterns(patterns)}
}

// Provenance records input paths, including YAML-only and inline fields that
// JSON output omits. Keep these conservative rules separate from output names.
func provenanceFields(t reflect.Type, marked bool, stack map[reflect.Type]bool, build func(reflect.Type) *jsonPlan) []jsonField {
	if stack[t] {
		return nil
	}
	stack[t] = true
	defer delete(stack, t)
	var fields []jsonField
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() && !field.Anonymous {
			continue
		}
		aliases := typeinfo.ResolveField(field)
		secret := marked || HasTag(field.Tag.Get(typeinfo.TagKey))
		child := build(field.Type)
		fields = append(fields, jsonField{candidates: aliases.Candidates, marked: secret, child: child})
		if !aliases.Flatten && !strings.Contains(","+field.Tag.Get("yaml")+",", ",inline,") {
			continue
		}
		ft := field.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			fields = append(fields, provenanceFields(ft, secret, stack, build)...)
		case reflect.Map:
			fields = append(fields, jsonField{marked: secret, child: child, inline: true})
		}
	}
	return fields
}

// Apply masks a detached JSON tree. The caller retains ownership of tree.
func (p *Plan) Apply(tree map[string]any, redactor Redactor) map[string]any {
	if redactor == nil {
		redactor = DefaultRedactor
	}
	value := p.root.redact(tree, "", redactor)
	out, ok := value.(map[string]any)
	if !ok {
		out = map[string]any{"$redacted": value}
	}
	redactPatterns(out, nil, p.patterns.tree, redactor)
	return out
}

// ApplyJSON follows actual JSON names, including literal dots in keys.
func ApplyJSON(tree map[string]any, t reflect.Type, redactor Redactor) map[string]any {
	return Compile(t, nil).Apply(tree, redactor)
}

var marshalerTypes = [...]reflect.Type{
	reflect.TypeFor[json.Marshaler](),
	reflect.TypeFor[encoding.TextMarshaler](),
}

func opaqueJSONType(t reflect.Type) bool {
	for _, iface := range marshalerTypes {
		if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
			return true
		}
	}
	return false
}

// PathHasSecrets checks provenance against the type, even when the final
// value omits a field or removes a list. Containers with secret descendants
// are masked as a whole because their historical shape can differ.
func PathHasSecrets(t reflect.Type, path string) bool {
	return Compile(t, nil).PathHasSecrets(path)
}

func (p *Plan) PathHasSecrets(path string) bool { return p.root.pathHasSecrets(path) }

func (p *jsonPlan) pathHasSecrets(path string) bool {
	if p == nil || !p.secrets {
		return false
	}
	if path == "" || p.opaque {
		return true
	}
	switch p.kind {
	case reflect.Struct:
		for _, field := range p.origins {
			if field.inline && (field.marked || field.child.pathHasSecrets(path)) {
				return true
			}
			var rest string
			matched := false
			for _, name := range field.candidates {
				if strings.EqualFold(path, name) {
					matched = true
					break
				}
				if len(path) > len(name) && path[len(name)] == '.' && strings.EqualFold(path[:len(name)], name) {
					rest, matched = path[len(name)+1:], true
					break
				}
			}
			if matched && (field.marked || field.child.pathHasSecrets(rest)) {
				return true
			}
		}
	case reflect.Map:
		// A dotted provenance key can represent several real path segments.
		for {
			_, rest, more := strings.Cut(path, ".")
			if p.elem.pathHasSecrets(rest) {
				return true
			}
			if !more {
				break
			}
			path = rest
		}
	case reflect.Slice, reflect.Array:
		_, rest, _ := strings.Cut(path, ".")
		return p.elem.pathHasSecrets(rest)
	}
	return false
}

func (p *jsonPlan) redact(node any, path string, redact Redactor) any {
	if node == nil || p == nil || !p.secrets {
		return node
	}
	if p.opaque {
		return redact(path, node)
	}
	switch p.kind {
	case reflect.Struct:
		if m, ok := node.(map[string]any); ok {
			for _, field := range p.fields {
				value, exists := m[field.name]
				if !exists || (!field.marked && !field.child.secrets) {
					continue
				}
				childPath := joinPath(path, field.name)
				if field.marked {
					m[field.name] = redact(childPath, value)
				} else {
					m[field.name] = field.child.redact(value, childPath, redact)
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if a, ok := node.([]any); ok {
			for i, value := range a {
				a[i] = p.elem.redact(value, joinPath(path, strconv.Itoa(i)), redact)
			}
		}
	case reflect.Map:
		if m, ok := node.(map[string]any); ok {
			for k, value := range m {
				m[k] = p.elem.redact(value, joinPath(path, k), redact)
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
