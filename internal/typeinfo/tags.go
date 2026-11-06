package typeinfo

import (
	"reflect"
	"strings"
)

// TagKey is FastConf's struct tag key for field metadata.
const TagKey = "fc"

// FieldNames is a struct field's configuration key: Primary is the name used in
// paths, InputAliases are every key accepted on input, and Flatten marks an
// untagged embedded struct whose fields share the parent path.
type FieldNames struct {
	Primary      string
	InputAliases []string
	Flatten      bool
}

// FieldNamesOf selects json → yaml → lower(name), flattening untagged embeds.
// InputAliases also include the exact Go name for typed-hook input aliases.
func FieldNamesOf(f reflect.StructField) FieldNames {
	jsonName := stripTag(f.Tag.Get("json"))
	yamlName := stripTag(f.Tag.Get("yaml"))
	out := FieldNames{}
	switch {
	case jsonName != "" && jsonName != "-":
		out.Primary = jsonName
	case yamlName != "" && yamlName != "-":
		out.Primary = yamlName
	case f.Anonymous:
		out.Flatten = true
	default:
		out.Primary = strings.ToLower(f.Name)
	}
	addCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == "-" {
			return
		}
		for _, existing := range out.InputAliases {
			if existing == s {
				return
			}
		}
		out.InputAliases = append(out.InputAliases, s)
	}
	addCandidate(jsonName)
	addCandidate(yamlName)
	addCandidate(strings.ToLower(f.Name))
	addCandidate(f.Name)
	return out
}

func stripTag(t string) string {
	if i := strings.IndexByte(t, ','); i >= 0 {
		return t[:i]
	}
	return t
}
