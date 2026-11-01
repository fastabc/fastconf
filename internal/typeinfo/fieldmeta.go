package typeinfo

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// fieldSpec stores a field's path, index and parsed fc constraints.
type fieldSpec struct {
	Path     string
	Index    []int
	Required bool
	Min      *float64
	Max      *float64
	OneOf    []string
}

var fieldMetaCache = NewCache[[]fieldSpec]()

// parseFieldTag parses required, min, max and oneof constraints. Other tokens, including defaults
// and descriptions, are ignored.
func parseFieldTag(tag string) fieldSpec {
	var fs fieldSpec
	for _, raw := range strings.Split(tag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		switch {
		case raw == "required":
			fs.Required = true
		case strings.HasPrefix(raw, "min="):
			if v, err := strconv.ParseFloat(strings.TrimPrefix(raw, "min="), 64); err == nil {
				fs.Min = &v
			}
		case strings.HasPrefix(raw, "max="):
			if v, err := strconv.ParseFloat(strings.TrimPrefix(raw, "max="), 64); err == nil {
				fs.Max = &v
			}
		case strings.HasPrefix(raw, "oneof="):
			body := strings.TrimPrefix(raw, "oneof=")
			fs.OneOf = strings.Split(body, "|")
		}
	}
	return fs
}

// fieldMetaFor returns the cached metadata plan after pointer indirection; non-struct types yield nil.
func fieldMetaFor(t reflect.Type) []fieldSpec {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return fieldMetaCache.GetOrCompute(t, func() []fieldSpec {
		var entries []fieldSpec
		Walk(t, func(path string, idx []int, f reflect.StructField) bool {
			tag := f.Tag.Get(TagKey)
			if tag != "" {
				spec := parseFieldTag(tag)
				if spec.Required || spec.Min != nil || spec.Max != nil || len(spec.OneOf) > 0 {
					spec.Path = path
					spec.Index = append([]int(nil), idx...)
					entries = append(entries, spec)
				}
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			return ft.Kind() == reflect.Struct
		})
		return entries
	})
}

// Violation describes one field-metadata constraint failure.
type Violation struct {
	Path string
	Msg  string
}

// CheckFieldMeta walks target and reports violations of required / min / max / oneof constraints
// declared on the type. Returns nil when no metadata is registered. The caller decides whether a
// single violation short-circuits the reload or whether to collect all of them for Plan().
func CheckFieldMeta(target any) []Violation {
	if target == nil {
		return nil
	}
	specs := fieldMetaFor(reflect.TypeOf(target))
	if len(specs) == 0 {
		return nil
	}
	rv := reflect.ValueOf(target)
	var out []Violation
fields:
	for _, spec := range specs {
		fv := fieldAt(rv, spec.Index)
		if !fv.IsValid() {
			continue
		}
		if spec.Required && fv.IsZero() {
			out = append(out, Violation{Path: spec.Path, Msg: spec.Path + ": required field is empty"})
			continue
		}
		for fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue fields
			}
			fv = fv.Elem()
		}
		if (spec.Min != nil || spec.Max != nil) && isNumeric(fv) {
			f := numericAsFloat(fv)
			if spec.Min != nil && f < *spec.Min {
				out = append(out, Violation{Path: spec.Path, Msg: fmt.Sprintf("%s: value %v below min %v", spec.Path, f, *spec.Min)})
			}
			if spec.Max != nil && f > *spec.Max {
				out = append(out, Violation{Path: spec.Path, Msg: fmt.Sprintf("%s: value %v above max %v", spec.Path, f, *spec.Max)})
			}
		}
		if len(spec.OneOf) > 0 {
			got := fmt.Sprint(fv.Interface())
			if !slices.Contains(spec.OneOf, got) {
				out = append(out, Violation{Path: spec.Path, Msg: fmt.Sprintf("%s: %q not in oneof %v", spec.Path, got, spec.OneOf)})
			}
		}
	}
	return out
}

func isNumeric(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func numericAsFloat(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	}
	return 0
}
