package typeinfo

import (
	"encoding"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// DefaultTagKey declares a literal default used only when an input key is absent.
const DefaultTagKey = "default"

// TagDefault is an immutable path plan for one absent-key default.
type TagDefault struct {
	Path       string
	Keys       []string
	Optional   []bool
	Raw        string
	foldNames  bool
	jsonFields [][]JSONField
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
			var fields []JSONField
			if yamlBridge {
				StructFields(t, EmbedYAML, func(f reflect.StructField, index []int) {
					if f.Tag.Get("yaml") != "-" {
						f.Index = append([]int(nil), index...)
						name := stripTag(f.Tag.Get("yaml"))
						if name == "" {
							name = strings.ToLower(f.Name)
						}
						fields = append(fields, JSONField{Name: name, Field: f})
					}
				})
			} else {
				fields = JSONFields(t)
			}
			for _, field := range fields {
				f, name := field.Field, field.Name
				optional := f.Type.Kind() == reflect.Pointer
				path := TagDefault{Keys: append(append([]string(nil), parent.Keys...), name), Optional: append(append([]bool(nil), parent.Optional...), optional)}
				path.jsonFields = append(append([][]JSONField(nil), parent.jsonFields...), fields)
				path.Path = strings.Join(path.Keys, ".")
				path.foldNames = !yamlBridge
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
		nodes := []map[string]any{root}
		for i, key := range d.Keys {
			var values []any
			for _, node := range nodes {
				// JSON decodes these maps in lexical key order. Several aliases
				// may contribute to the same object; inspect them all before
				// adding a default that could overwrite an explicit child.
				for _, actual := range slices.Sorted(maps.Keys(node)) {
					if actual == key || (d.foldNames && defaultJSONName(d.jsonFields[i], actual) == key) {
						values = append(values, node[actual])
					}
				}
			}
			node := nodes[len(nodes)-1]
			if i == len(d.Keys)-1 {
				if len(values) == 0 {
					node[key] = d.Raw
					if record != nil {
						record(d.Path, d.Raw)
					}
				}
				break
			}
			if len(values) == 0 {
				if d.Optional[i] && !force {
					break
				}
				child := map[string]any{}
				node[key] = child
				values = append(values, child)
			}
			nodes = nil
			for _, value := range values {
				if value == nil && d.Optional[i] {
					// A later null resets a pointer; prior objects no longer
					// contribute to the decoded field.
					nodes = nil
				} else if child, ok := value.(map[string]any); ok {
					nodes = append(nodes, child)
				}
			}
			if len(nodes) == 0 {
				break
			}
		}
	}
}

// JSON prefers an exact field name, then the first declared folded match.
func defaultJSONName(fields []JSONField, key string) string {
	var matched *JSONField
	for i := range fields {
		f := &fields[i]
		if f.Name == key {
			return f.Name
		}
		if strings.EqualFold(f.Name, key) && (matched == nil || slices.Compare(f.Field.Index, matched.Field.Index) < 0) {
			matched = f
		}
	}
	if matched != nil {
		return matched.Name
	}
	return ""
}
