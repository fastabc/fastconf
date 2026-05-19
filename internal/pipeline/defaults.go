// Package pipeline holds reload-pipeline helpers that are pure functions
// of *T or the merged map[string]any. The root fastconf package keeps
// the stage definitions (which access Manager[T]/options) and delegates
// the per-field reflection walks here.
package pipeline

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// defaultsCache caches the parsed default-tag plan for each *T so the
// hot reload path stays allocation-free after the first invocation.
var defaultsCache = typeinfo.NewCache[[]defaultEntry]()

type defaultEntry struct {
	index  []int
	tagVal string
}

// ApplyStructDefaults populates zero-valued fields of *t whose `fastconf`
// tag declares a "default=..." token. Returns an error if a tag value
// cannot be parsed for the declared kind.
func ApplyStructDefaults[T any](t *T) error {
	if t == nil {
		return nil
	}
	v := reflect.ValueOf(t).Elem()
	if !v.IsValid() {
		return nil
	}
	plan := planForType(v.Type())
	for _, e := range plan {
		fv := v.FieldByIndex(e.index)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}
		if !fv.IsZero() {
			continue
		}
		if err := assignFromTag(fv, e.tagVal); err != nil {
			return fmt.Errorf("fastconf default %q: %w", e.tagVal, err)
		}
	}
	return nil
}

func planForType(t reflect.Type) []defaultEntry {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return defaultsCache.GetOrCompute(t, func() []defaultEntry {
		var entries []defaultEntry
		typeinfo.Walk(t, typeinfo.WalkFunc(func(_ string, idx []int, f reflect.StructField, _ *reflect.Type) bool {
			if val, ok := defaultTagValue(f.Tag.Get("fastconf")); ok {
				entries = append(entries, defaultEntry{
					index:  append([]int(nil), idx...),
					tagVal: val,
				})
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

func defaultTagValue(tag string) (string, bool) {
	if tag == "" {
		return "", false
	}
	for _, p := range strings.Split(tag, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(p), "default="); ok {
			return v, true
		}
	}
	return "", false
}

func assignFromTag(fv reflect.Value, raw string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("unsupported default kind %s", fv.Kind())
	}
	return nil
}
