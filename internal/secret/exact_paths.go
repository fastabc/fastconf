package secret

import (
	"reflect"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// jsonPaths translates input aliases and YAML inline fields to diagnostic JSON
// names. Ambiguous aliases mask every possible output, preserving secrecy.
func jsonPaths(t reflect.Type, path []string, depth int) [][]string {
	if t == nil || depth > maxTreeDepth {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if len(path) == 0 || opaqueJSONType(t) {
		return [][]string{nil}
	}
	switch t.Kind() {
	case reflect.Struct:
		var outputs [][]string
		for _, f := range typeinfo.JSONFields(t) {
			for _, rest := range inputRemainders(t, f.Field.Index, path) {
				for _, child := range jsonPaths(f.Field.Type, rest, depth+1) {
					outputs = append(outputs, append([]string{f.Name}, child...))
				}
			}
		}
		return outputs
	case reflect.Map, reflect.Slice, reflect.Array:
		var outputs [][]string
		for _, child := range jsonPaths(t.Elem(), path[1:], depth+1) {
			outputs = append(outputs, append([]string{path[0]}, child...))
		}
		return outputs
	case reflect.Interface:
		// The decode bridge fills interfaces with ordinary JSON-shaped values.
		// Keep their literal map/list segments rather than changing container shape.
		return [][]string{append([]string(nil), path...)}
	default:
		return nil
	}
}

func inputRemainders(t reflect.Type, index []int, path []string) [][]string {
	if len(index) == 0 {
		return [][]string{path}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	f := t.Field(index[0])
	names := typeinfo.FieldNamesOf(f)
	var remainders [][]string
	if (len(index) > 1 && names.Flatten) || typeinfo.YAMLInline(f.Tag.Get("yaml")) {
		if len(index) == 1 {
			remainders = append(remainders, path)
		} else {
			remainders = append(remainders, inputRemainders(f.Type, index[1:], path)...)
		}
	}
	if len(path) > 0 {
		for _, alias := range names.InputAliases {
			if strings.EqualFold(alias, path[0]) {
				if len(index) == 1 {
					remainders = append(remainders, path[1:])
				} else {
					remainders = append(remainders, inputRemainders(f.Type, index[1:], path[1:])...)
				}
				break
			}
		}
	}
	return remainders
}
