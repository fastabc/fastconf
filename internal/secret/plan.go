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
	root     *typePlan
	patterns patternPlan
}

type planField struct {
	name       string
	candidates []string
	marked     bool
	child      *typePlan
	inline     bool
}

type typePlan struct {
	kind            reflect.Kind
	opaque, secrets bool
	fields          []planField
	origins         []planField
	elem            *typePlan
}

// Compile builds metadata once, including cycles in recursive types.
func Compile(t reflect.Type, patterns []string) *Plan {
	memo := map[reflect.Type]*typePlan{}
	var build func(reflect.Type) *typePlan
	build = func(t reflect.Type) *typePlan {
		if t == nil {
			return nil
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if p := memo[t]; p != nil {
			return p
		}
		p := &typePlan{kind: t.Kind(), opaque: opaqueJSONType(t), secrets: hasSecrets(t)}
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
				p.fields = append(p.fields, planField{name: field.Name, marked: marked, child: build(ft)})
			}
			p.origins = provenanceFields(t, false, map[reflect.Type]bool{}, build)
		case reflect.Map, reflect.Array, reflect.Slice:
			p.elem = build(t.Elem())
		}
		return p
	}
	return &Plan{root: build(t), patterns: compilePatterns(patterns)}
}

// provenanceFields retains parent aliases and inherited secret flags alongside flattened
// children. Input paths also include YAML-only fields and inline maps absent from JSON output.
func provenanceFields(t reflect.Type, marked bool, stack map[reflect.Type]bool, build func(reflect.Type) *typePlan) []planField {
	if stack[t] {
		return nil
	}
	stack[t] = true
	defer delete(stack, t)
	var fields []planField
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() && !field.Anonymous {
			continue
		}
		aliases := typeinfo.FieldNamesOf(field)
		secret := marked || HasTag(field.Tag.Get(typeinfo.TagKey))
		child := build(field.Type)
		fields = append(fields, planField{candidates: aliases.InputAliases, marked: secret, child: child})
		if !aliases.Flatten && !typeinfo.YAMLInline(field.Tag.Get("yaml")) {
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
			fields = append(fields, planField{marked: secret, child: child, inline: true})
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

var marshalerTypes = [...]reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()}

func opaqueJSONType(t reflect.Type) bool {
	for _, iface := range marshalerTypes {
		if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
			return true
		}
	}
	return false
}

// PatternsMatch reports whether a WithSecretPaths pattern covers path or any
// path inside value, treating dots in flattened provenance paths conservatively.
func (p *Plan) PatternsMatch(path string, value any) bool {
	return p.patterns.matchesValue(value, path)
}

// PathHasSecrets checks provenance against the type, even when the final
// value omits a field or removes a list. Containers with secret descendants
// are masked as a whole because their historical shape can differ.
func (p *Plan) PathHasSecrets(path string) bool { return p.root.pathHasSecrets(path) }

func (p *typePlan) pathHasSecrets(path string) bool {
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

func (p *typePlan) redact(node any, path string, redact Redactor) any {
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

// joinPath avoids a leading separator for root containers, preserving exact
// path matches in user redactors.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func hasSecrets(t reflect.Type) bool {
	return typeinfo.Reaches(t, func(t reflect.Type) bool {
		if t.Kind() != reflect.Struct {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if (f.IsExported() || f.Anonymous) && HasTag(f.Tag.Get(typeinfo.TagKey)) {
				return true
			}
		}
		return false
	})
}
