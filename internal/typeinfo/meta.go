package typeinfo

import (
	"reflect"
	"strings"
)

// Meta describes generation metadata shared by Schema, YAML and env examples.
type Meta struct {
	Desc             string
	Default          string
	HasDefault       bool
	Required, Secret bool
	Min, Max         *float64
	OneOf            []string
}

// FieldMeta reads desc/default tags, falling back to the corresponding fc tokens.
func FieldMeta(f reflect.StructField) Meta {
	tag := f.Tag.Get(TagKey)
	constraints := parseFieldTag(tag)
	meta := Meta{Required: constraints.Required, Min: constraints.Min, Max: constraints.Max, OneOf: constraints.OneOf}
	for token := range strings.SplitSeq(tag, ",") {
		token = strings.TrimSpace(token)
		if token == "secret" {
			meta.Secret = true
		}
		if v, ok := strings.CutPrefix(token, "desc="); ok {
			meta.Desc = v
		}
	}
	if desc, ok := f.Tag.Lookup("desc"); ok {
		meta.Desc = desc
	}
	meta.Default, meta.HasDefault = f.Tag.Lookup(DefaultTagKey)
	if !meta.HasDefault {
		meta.Default, meta.HasDefault = defaultTagValue(tag)
	}
	return meta
}
