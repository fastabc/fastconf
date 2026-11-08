package typeinfo

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// DefaultTagKey declares a literal default used only when an input key is absent.
const DefaultTagKey = "default"

// TagDefault is an immutable path plan for one absent-key default.
type TagDefault struct {
	Path     string
	Keys     []string
	Aliases  [][]string
	Optional []bool
	Raw      string
}

// TagDefaultsFor validates declarations and selects paths using the decode bridge's fields.
func TagDefaultsFor(t reflect.Type, yamlBridge bool) ([]TagDefault, error) {
	var defs []TagDefault
	type visitKey struct {
		typ        reflect.Type
		collection bool
	}
	stack := map[visitKey]bool{}
	var visit func(reflect.Type, TagDefault, bool) error
	visit = func(t reflect.Type, parent TagDefault, collection bool) error {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		key := visitKey{t, collection}
		if stack[key] {
			return nil
		}
		stack[key] = true
		defer delete(stack, key)
		switch t.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			return visit(t.Elem(), parent, true)
		case reflect.Struct:
			var fields []reflect.StructField
			if yamlBridge {
				StructFields(t, EmbedYAML, func(f reflect.StructField, index []int) {
					if f.Tag.Get("yaml") != "-" {
						f.Index = append([]int(nil), index...)
						fields = append(fields, f)
					}
				})
			} else {
				for _, f := range JSONFields(t) {
					fields = append(fields, f.Field)
				}
			}
			for _, f := range fields {
				names := FieldNamesOf(f)
				name := names.Primary
				if yamlBridge {
					name = stripTag(f.Tag.Get("yaml"))
					if name == "" {
						name = strings.ToLower(f.Name)
					}
				}
				optional := f.Type.Kind() == reflect.Pointer
				path := TagDefault{Keys: append(append([]string(nil), parent.Keys...), name), Aliases: append(append([][]string(nil), parent.Aliases...), names.InputAliases), Optional: append(append([]bool(nil), parent.Optional...), optional)}
				path.Path = strings.Join(path.Keys, ".")
				raw, has := f.Tag.Lookup(DefaultTagKey)
				if has {
					if _, conflict := defaultTagValue(f.Tag.Get(TagKey)); conflict {
						return nilDefaultError(path.Path, "conflicts with fc default")
					}
					if collection {
						return nilDefaultError(path.Path, "not supported inside collection elements")
					}
					ft := f.Type
					for ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if (ft.Kind() == reflect.Struct || ft.Kind() == reflect.Map) && !defaultUnmarshaler(f.Type) {
						return nilDefaultError(path.Path, "object defaults require a custom unmarshaler")
					}
					path.Raw = raw
					defs = append(defs, path)
				}
				if err := visit(f.Type, path, collection); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if t != nil {
		if err := visit(t, TagDefault{}, false); err != nil {
			return nil, err
		}
	}
	return defs, nil
}

func nilDefaultError(path, reason string) error {
	return fmt.Errorf("default tag %s: %s", path, reason)
}

func defaultUnmarshaler(t reflect.Type) bool {
	for _, iface := range []reflect.Type{reflect.TypeFor[encoding.TextUnmarshaler](), reflect.TypeFor[json.Unmarshaler]()} {
		if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
			return true
		}
	}
	return false
}

// InjectTagDefaults fills missing leaves. Optional object parents are created
// only during forced construction-time validation. Explicit nulls remain present.
func InjectTagDefaults(root map[string]any, defs []TagDefault, force bool, record func(path, raw string)) {
	for _, d := range defs {
		node := root
		for i, key := range d.Keys {
			present := false
			for _, alias := range d.Aliases[i] {
				if _, ok := node[alias]; ok {
					key = alias
					present = true
					break
				}
			}
			if i == len(d.Keys)-1 {
				if !present {
					node[key] = d.Raw
					if record != nil {
						record(d.Path, d.Raw)
					}
				}
				break
			}
			if !present {
				if d.Optional[i] && !force {
					break
				}
				node[key] = map[string]any{}
			}
			next, ok := node[key].(map[string]any)
			if !ok {
				break
			}
			node = next
		}
	}
}
