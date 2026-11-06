package typeinfo

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// defaultsCache caches the parsed default-tag plan for each *T so the hot reload path stays
// allocation-free after the first invocation.
var defaultsCache = NewCache[[]defaultEntry]()

type defaultEntry struct {
	index []int
	raw   string
}

// ApplyStructDefaults populates zero-valued fields of *t whose `fc` tag declares a "default=..."
// token. Returns an error if a tag value cannot be parsed for the declared kind.
func ApplyStructDefaults[T any](t *T) error {
	if t == nil {
		return nil
	}
	v := reflect.ValueOf(t).Elem()
	if !v.IsValid() {
		return nil
	}
	plan := defaultsPlanFor(v.Type())
	for _, e := range plan {
		fv := fieldAt(v, e.index)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}
		if !fv.IsZero() {
			continue
		}
		if err := assignFromTag(fv, e.raw); err != nil {
			return fmt.Errorf("fastconf default %q: %w", e.raw, err)
		}
	}
	return nil
}

func defaultsPlanFor(t reflect.Type) []defaultEntry {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return defaultsCache.GetOrCompute(t, func() []defaultEntry {
		var entries []defaultEntry
		Walk(t, func(_ string, idx []int, f reflect.StructField) bool {
			if val, ok := defaultTagValue(f.Tag.Get(TagKey)); ok {
				entries = append(entries, defaultEntry{
					index: append([]int(nil), idx...),
					raw:   val,
				})
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
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("unsupported default kind %s", fv.Kind())
	}
	return nil
}
