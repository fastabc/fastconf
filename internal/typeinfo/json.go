package typeinfo

import (
	"reflect"
	"sort"
	"strings"
	"unicode"
)

// JSONField describes a field selected by encoding/json's naming, embedding, depth and
// tagged-field dominance rules. Index is relative to the root type.
type JSONField struct {
	Name   string
	Field  reflect.StructField
	tagged bool
}

// JSONFields returns an independent description of the serialized fields. Input aliases (including
// YAML tags) deliberately do not affect JSON output.
func JSONFields(t reflect.Type) []JSONField {
	var candidates []JSONField
	StructFields(t, EmbedJSON, func(f reflect.StructField, index []int) {
		tag := f.Tag.Get("json")
		if tag == "-" {
			return
		}
		name := stripTag(tag)
		if !validJSONName(name) {
			name = ""
		}
		tagged := name != ""
		if name == "" {
			name = f.Name
		}
		f.Index = append([]int(nil), index...)
		candidates = append(candidates, JSONField{Name: name, Field: f, tagged: tagged})
	})

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if len(a.Field.Index) != len(b.Field.Index) {
			return len(a.Field.Index) < len(b.Field.Index)
		}
		return a.tagged && !b.tagged
	})
	var out []JSONField
	for i := 0; i < len(candidates); {
		j := i + 1
		for j < len(candidates) && candidates[j].Name == candidates[i].Name {
			j++
		}
		a := candidates[i]
		if j == i+1 || len(a.Field.Index) != len(candidates[i+1].Field.Index) || a.tagged != candidates[i+1].tagged {
			out = append(out, a)
		}
		i = j
	}
	return out
}

// Match encoding/json's accepted tag names; invalid tags fall back to Name.
func validJSONName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) {
			return false
		}
	}
	return true
}
