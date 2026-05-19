package pipeline

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// FieldSpec captures the structured metadata parsed from a single
// `fastconf` tag (independent of the legacy `default=` and `secret`
// flags which keep their own walkers).
type FieldSpec struct {
	Path     string
	Index    []int
	Default  string
	Required bool
	Min      *float64
	Max      *float64
	OneOf    []string
	Desc     string
}

var fieldMetaCache = typeinfo.NewCache[[]FieldSpec]()

// ParseFieldTag converts a tag string like
// "default=info,required,min=1,max=100,oneof=info|warn|error,desc=log level"
// into a FieldSpec. Unknown tokens are silently ignored so older code
// keeps compiling.
func ParseFieldTag(tag string) FieldSpec {
	var fs FieldSpec
	for _, raw := range strings.Split(tag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		switch {
		case raw == "required":
			fs.Required = true
		case raw == "secret":
			// handled in internal/secret
		case strings.HasPrefix(raw, "default="):
			fs.Default = strings.TrimPrefix(raw, "default=")
		case strings.HasPrefix(raw, "desc="):
			fs.Desc = strings.TrimPrefix(raw, "desc=")
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

// FieldMetaFor returns the cached metadata plan for t. Pointer / non-struct
// types yield nil.
func FieldMetaFor(t reflect.Type) []FieldSpec {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return fieldMetaCache.GetOrCompute(t, func() []FieldSpec {
		var entries []FieldSpec
		typeinfo.Walk(t, typeinfo.WalkFunc(func(path string, idx []int, f reflect.StructField, _ *reflect.Type) bool {
			tag := f.Tag.Get("fastconf")
			if tag != "" {
				spec := ParseFieldTag(tag)
				if spec.Required || spec.Min != nil || spec.Max != nil || len(spec.OneOf) > 0 || spec.Default != "" || spec.Desc != "" {
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
		}))
		return entries
	})
}

// Violation describes one field-metadata constraint failure.
type Violation struct {
	Path string
	Msg  string
}

// CheckFieldMeta walks target and reports violations of required / min /
// max / oneof constraints declared on the type. Returns nil when no
// metadata is registered. The caller decides whether a single violation
// short-circuits the reload or whether to collect all of them for Plan().
func CheckFieldMeta(target any) []Violation {
	if target == nil {
		return nil
	}
	specs := FieldMetaFor(reflect.TypeOf(target))
	if len(specs) == 0 {
		return nil
	}
	rv := reflect.ValueOf(target).Elem()
	var out []Violation
	for _, spec := range specs {
		fv := rv.FieldByIndex(spec.Index)
		if !fv.IsValid() {
			continue
		}
		if spec.Required && fv.IsZero() {
			out = append(out, Violation{Path: spec.Path, Msg: spec.Path + ": required field is empty"})
			continue
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
