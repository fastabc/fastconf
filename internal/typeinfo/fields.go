package typeinfo

import (
	"reflect"
	"strings"
)

// Embed selects embedding rules; values may be combined.
type Embed uint8

const (
	EmbedJSON Embed = 1 << iota // Untagged anonymous structs, including unexported embeds.
	EmbedYAML                   // Exported fields tagged yaml:",inline".
)

// YAMLInline reports whether the YAML tag options include inline.
func YAMLInline(tag string) bool {
	_, flags, _ := strings.Cut(tag, ",")
	for flag := range strings.SplitSeq(flags, ",") {
		if flag == "inline" {
			return true
		}
	}
	return false
}

// StructFields visits fields after pointer indirection, flattening selected struct embeds.
// EmbedJSON also admits unexported anonymous structs; other unexported fields are skipped.
// Callers resolve names and ignored tags. index is relative to t and must not be retained.
// Embedded cycles are cut.
func StructFields(t reflect.Type, embed Embed, fn func(reflect.StructField, []int)) {
	stack := map[reflect.Type]bool{}
	var visit func(reflect.Type, []int)
	visit = func(t reflect.Type, index []int) {
		if t == nil {
			return
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || stack[t] {
			return
		}
		stack[t] = true
		defer delete(stack, t)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if !f.IsExported() && (embed&EmbedJSON == 0 || !f.Anonymous || ft.Kind() != reflect.Struct) {
				continue
			}
			next := append(index, i)
			jsonTag := f.Tag.Get("json")
			jsonEmbed := embed&EmbedJSON != 0 && f.Anonymous && jsonTag != "-" && !validJSONName(stripTag(jsonTag))
			yamlEmbed := embed&EmbedYAML != 0 && YAMLInline(f.Tag.Get("yaml"))
			if ft.Kind() == reflect.Struct && (jsonEmbed || yamlEmbed) {
				visit(ft, next)
			} else {
				fn(f, next)
			}
		}
	}
	visit(t, nil)
}

// Reaches tests t and types reachable through pointers, elements, map keys/values,
// and exported or embedded fields. Each type is tested once.
func Reaches(t reflect.Type, pred func(reflect.Type) bool) bool {
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type) bool
	visit = func(t reflect.Type) bool {
		if t == nil || seen[t] {
			return false
		}
		seen[t] = true
		if pred(t) {
			return true
		}
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			return visit(t.Elem())
		case reflect.Map:
			return visit(t.Key()) || visit(t.Elem())
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if (f.IsExported() || f.Anonymous) && visit(f.Type) {
					return true
				}
			}
		}
		return false
	}
	return visit(t)
}
