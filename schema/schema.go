// Package schema generates configuration documentation from struct types and tags.
package schema

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

var durationType = reflect.TypeFor[time.Duration]()

func baseType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}
func textType(t reflect.Type) bool   { return implements(t, reflect.TypeFor[encoding.TextUnmarshaler]()) }
func jsonType(t reflect.Type) bool   { return implements(t, reflect.TypeFor[json.Unmarshaler]()) }
func scalarType(t reflect.Type) bool { return t == durationType || textType(t) || jsonType(t) }
func fields(t reflect.Type, declarationOrder bool) []typeinfo.JSONField {
	fs := typeinfo.JSONFields(t)
	if declarationOrder {
		slices.SortFunc(fs, func(a, b typeinfo.JSONField) int { return slices.Compare(a.Field.Index, b.Field.Index) })
	}
	return fs
}
func checkType(t reflect.Type) error {
	if t == nil {
		return fmt.Errorf("schema: nil type")
	}
	if baseType(t).Kind() != reflect.Struct {
		return fmt.Errorf("schema: expected struct type")
	}
	return nil
}

// Legacy fc duration defaults are integer nanoseconds, while generated input
// documents use the duration strings accepted by the default typed hooks.
func fieldMeta(f reflect.StructField) (typeinfo.Meta, error) {
	meta := typeinfo.FieldMeta(f)
	if meta.HasDefault && baseType(f.Type) == durationType {
		if _, literal := f.Tag.Lookup(typeinfo.DefaultTagKey); !literal {
			n, err := strconv.ParseInt(meta.Default, 10, 64)
			if err != nil {
				return meta, fmt.Errorf("schema: %s default: %w", f.Name, err)
			}
			meta.Default = time.Duration(n).String()
		}
	}
	return meta, nil
}

// defaultValue converts literal tag values into their schema/example wire form.
func defaultValue(t reflect.Type, raw any) (any, error) {
	t = baseType(t)
	if t == durationType {
		s := fmt.Sprint(raw)
		if _, err := time.ParseDuration(s); err != nil {
			return nil, err
		}
		return s, nil
	}
	if textType(t) || jsonType(t) {
		return raw, nil
	}
	s := fmt.Sprint(raw)
	switch t.Kind() {
	case reflect.String:
		if _, ok := raw.(string); !ok {
			return nil, fmt.Errorf("expected string default")
		}
		return raw, nil
	case reflect.Bool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off", "":
			return false, nil
		}
		return nil, fmt.Errorf("invalid boolean default")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.ParseInt(strings.TrimSpace(s), 10, t.Bits())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.ParseUint(strings.TrimSpace(s), 10, t.Bits())
	case reflect.Float32, reflect.Float64:
		return strconv.ParseFloat(strings.TrimSpace(s), t.Bits())
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return raw, nil
		}
		list, ok := raw.([]any)
		if !ok {
			value, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("expected list default")
			}
			value = strings.TrimSpace(value)
			list = []any{}
			if strings.HasPrefix(value, "[") {
				dec := json.NewDecoder(strings.NewReader(value))
				dec.UseNumber()
				if err := dec.Decode(&list); err != nil {
					return nil, err
				}
				var extra any
				if err := dec.Decode(&extra); err != io.EOF {
					return nil, fmt.Errorf("expected one JSON array")
				}
			} else if value != "" {
				for part := range strings.SplitSeq(value, ",") {
					list = append(list, strings.TrimSpace(part))
				}
			}
		}
		for i, v := range list {
			converted, err := defaultValue(t.Elem(), v)
			if err != nil {
				return nil, err
			}
			list[i] = converted
		}
		return list, nil
	}
	return nil, fmt.Errorf("unsupported default type %s", t)
}
func comment(meta typeinfo.Meta) string {
	flags := []string{}
	if meta.Required {
		flags = append(flags, "required")
	}
	if meta.Secret {
		flags = append(flags, "secret")
	}
	if len(flags) == 0 {
		return meta.Desc
	}
	suffix := "(" + strings.Join(flags, ", ") + ")"
	if meta.Desc == "" {
		return suffix
	}
	return meta.Desc + " " + suffix
}
