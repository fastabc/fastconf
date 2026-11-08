package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

type schemaNode struct {
	Schema               string      `json:"$schema,omitempty"`
	Type                 string      `json:"type,omitempty"`
	Description          string      `json:"description,omitempty"`
	Default              any         `json:"default,omitempty"`
	Enum                 []any       `json:"enum,omitempty"`
	Minimum              *float64    `json:"minimum,omitempty"`
	Maximum              *float64    `json:"maximum,omitempty"`
	WriteOnly            bool        `json:"writeOnly,omitempty"`
	Pattern              string      `json:"pattern,omitempty"`
	ContentEncoding      string      `json:"contentEncoding,omitempty"`
	Properties           properties  `json:"properties,omitempty"`
	Required             []string    `json:"required,omitempty"`
	Items                *schemaNode `json:"items,omitempty"`
	AdditionalProperties *schemaNode `json:"additionalProperties,omitempty"`
}
type property struct {
	name string
	node *schemaNode
}
type properties []property

func (p properties) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(f.name)
		b.Write(key)
		b.WriteByte(':')
		value, err := json.Marshal(f.node)
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// JSONSchema returns a Draft 2020-12 schema with JSON field names. Recursive
// occurrences are represented as objects; additional fields remain permitted.
func JSONSchema(t reflect.Type) ([]byte, error) {
	if err := checkType(t); err != nil {
		return nil, err
	}
	root, err := buildSchema(t, map[reflect.Type]bool{})
	if err != nil {
		return nil, err
	}
	root.Schema = "https://json-schema.org/draft/2020-12/schema"
	return json.MarshalIndent(root, "", "  ")
}
func buildSchema(t reflect.Type, stack map[reflect.Type]bool) (*schemaNode, error) {
	t = baseType(t)
	n := &schemaNode{}
	switch {
	case t == durationType:
		n.Type = "string"
		n.Pattern = `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`
		return n, nil
	case jsonType(t):
		return n, nil
	case textType(t):
		n.Type = "string"
		return n, nil
	}
	if stack[t] {
		n.Type = "object"
		return n, nil
	}
	stack[t] = true
	defer delete(stack, t)
	var err error
	switch t.Kind() {
	case reflect.String:
		n.Type = "string"
	case reflect.Bool:
		n.Type = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n.Type = "integer"
	case reflect.Float32, reflect.Float64:
		n.Type = "number"
	case reflect.Interface:
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			n.Type = "string"
			n.ContentEncoding = "base64"
			break
		}
		n.Type = "array"
		n.Items, err = buildSchema(t.Elem(), stack)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("schema: map keys must be strings")
		}
		n.Type = "object"
		n.AdditionalProperties, err = buildSchema(t.Elem(), stack)
	case reflect.Struct:
		n.Type = "object"
		for _, f := range fields(t, false) {
			child, e := buildSchema(f.Field.Type, stack)
			if e != nil {
				return nil, e
			}
			meta := typeinfo.FieldMeta(f.Field)
			child.Description = meta.Desc
			child.WriteOnly = meta.Secret
			if child.Type == "integer" || child.Type == "number" {
				child.Minimum = meta.Min
				child.Maximum = meta.Max
			}
			if meta.HasDefault {
				child.Default, e = defaultValue(f.Field.Type, meta.Default)
				if e != nil {
					return nil, fmt.Errorf("schema: %s default: %w", f.Name, e)
				}
			}
			for _, v := range meta.OneOf {
				enum, e := defaultValue(f.Field.Type, v)
				if e != nil {
					return nil, e
				}
				child.Enum = append(child.Enum, enum)
			}
			n.Properties = append(n.Properties, property{f.Name, child})
			if meta.Required {
				n.Required = append(n.Required, f.Name)
			}
		}
	default:
		return nil, fmt.Errorf("schema: unsupported type %s", t)
	}
	return n, err
}
