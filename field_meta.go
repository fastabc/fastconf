package fastconf

// Structured field metadata via the existing `fastconf` tag.
//
// Today only `default=...` and `secret` are recognised on the tag. This
// file extends the parser to also accept `required`, `min=`, `max=`,
// `oneof=a|b|c`, and `desc=...`, then adds a pipeline stage that
// checks `required` / `min` / `max` / `oneof` against the decoded *T.
//
// The walker is the same shared typeinfo.Walk used by defaults/secrets,
// so per-T work is paid once at init and cached.

import (
	"context"
	"fmt"
	"reflect"
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

// parseFieldTag converts a tag string like
// "default=info,required,min=1,max=100,oneof=info|warn|error,desc=log level"
// into a FieldSpec. Unknown tokens are silently ignored so older code
// keeps compiling.
func parseFieldTag(tag string) FieldSpec {
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
			// handled elsewhere (secret.go)
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

// fieldMetaFor returns the cached metadata plan for t.
func fieldMetaFor(t reflect.Type) []FieldSpec {
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
				spec := parseFieldTag(tag)
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

// runFieldMetaCheck enforces required / min / max / oneof on the
// decoded *T. It runs in the validate stage path so violations join
// the same failure-safe semantics as user-supplied validators. In dry
// run (Plan) mode it accumulates findings into pc.reports instead of
// short-circuiting.
func runFieldMetaCheck[T any](_ context.Context, _ *Manager[T], pc *pipelineCtx[T]) error {
	if pc.target == nil {
		return nil
	}
	specs := fieldMetaFor(reflect.TypeOf(pc.target))
	if len(specs) == 0 {
		return nil
	}
	rv := reflect.ValueOf(pc.target).Elem()
	var firstErr error
	report := func(msg string) {
		if pc.dryRun {
			pc.reports = append(pc.reports, ValidatorReport{
				Name: "fastconf:field-meta",
				Err:  fmt.Errorf("%w: %s", ErrValidator, msg),
			})
			return
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%w: %s", ErrValidator, msg)
		}
	}
	for _, spec := range specs {
		fv := rv.FieldByIndex(spec.Index)
		if !fv.IsValid() {
			continue
		}
		if spec.Required && fv.IsZero() {
			report(spec.Path + ": required field is empty")
			continue
		}
		if (spec.Min != nil || spec.Max != nil) && isNumeric(fv) {
			f := numericAsFloat(fv)
			if spec.Min != nil && f < *spec.Min {
				report(fmt.Sprintf("%s: value %v below min %v", spec.Path, f, *spec.Min))
			}
			if spec.Max != nil && f > *spec.Max {
				report(fmt.Sprintf("%s: value %v above max %v", spec.Path, f, *spec.Max))
			}
		}
		if len(spec.OneOf) > 0 {
			got := fmt.Sprint(fv.Interface())
			ok := false
			for _, allowed := range spec.OneOf {
				if got == allowed {
					ok = true
					break
				}
			}
			if !ok {
				report(fmt.Sprintf("%s: %q not in oneof %v", spec.Path, got, spec.OneOf))
			}
		}
	}
	return firstErr
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
