package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
	"gopkg.in/yaml.v3"
)

// ExampleYAML returns an annotated YAML example in struct declaration order.
func ExampleYAML(t reflect.Type) ([]byte, error) {
	if err := checkType(t); err != nil {
		return nil, err
	}
	root, err := exampleNode(t, typeinfo.Meta{}, map[reflect.Type]bool{})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func exampleNode(t reflect.Type, meta typeinfo.Meta, stack map[reflect.Type]bool) (*yaml.Node, error) {
	t = baseType(t)
	if meta.HasDefault {
		value, err := defaultValue(t, meta.Default)
		if err != nil {
			return nil, err
		}
		n := &yaml.Node{}
		if err := n.Encode(value); err != nil {
			return nil, err
		}
		if n.Kind == yaml.SequenceNode {
			n.Style = yaml.FlowStyle
		}
		return n, nil
	}
	if t == durationType {
		return scalarNode("0s"), nil
	}
	if scalarType(t) {
		return scalarNode(""), nil
	}
	if stack[t] {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	stack[t] = true
	defer delete(stack, t)
	switch t.Kind() {
	case reflect.Struct:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, f := range fields(t, true) {
			meta, err := fieldMeta(f.Field)
			if err != nil {
				return nil, err
			}
			child, err := exampleNode(f.Field.Type, meta, stack)
			if err != nil {
				return nil, fmt.Errorf("schema: %s: %w", f.Name, err)
			}
			key := scalarNode(f.Name)
			key.HeadComment = comment(meta)
			n.Content = append(n.Content, key, child)
		}
		return n, nil
	case reflect.Map:
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return scalarNode(""), nil
		}
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		if t.Kind() == reflect.Array {
			for range t.Len() {
				child, err := exampleNode(t.Elem(), typeinfo.Meta{}, stack)
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, child)
			}
		}
		return n, nil
	default:
		n := &yaml.Node{}
		if err := n.Encode(reflect.Zero(t).Interface()); err != nil {
			return nil, err
		}
		return n, nil
	}
}
func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// ExampleEnv returns annotated environment defaults. Fields whose names contain
// the separator are skipped with an addressability comment.
func ExampleEnv(t reflect.Type, prefix, sep string) ([]byte, error) {
	if err := checkType(t); err != nil {
		return nil, err
	}
	if sep == "" {
		return nil, fmt.Errorf("schema: empty env separator")
	}
	var b strings.Builder
	stack := map[reflect.Type]bool{}
	var walk func(reflect.Type, []string) error
	walk = func(t reflect.Type, path []string) error {
		t = baseType(t)
		if stack[t] {
			return nil
		}
		stack[t] = true
		defer delete(stack, t)
		for _, f := range fields(t, true) {
			keys := append(append([]string(nil), path...), f.Name)
			if strings.Contains(f.Name, sep) {
				fmt.Fprintf(&b, "# %s: not addressable with separator %q; use %q\n", strings.Join(keys, "."), sep, sep+sep)
				continue
			}
			meta, err := fieldMeta(f.Field)
			if err != nil {
				return err
			}
			ft := baseType(f.Field.Type)
			if ft.Kind() == reflect.Struct && !scalarType(ft) && !meta.HasDefault {
				if err := walk(ft, keys); err != nil {
					return err
				}
				continue
			}
			if c := comment(meta); c != "" {
				for line := range strings.SplitSeq(c, "\n") {
					fmt.Fprintf(&b, "# %s\n", line)
				}
			}
			value := ""
			if meta.HasDefault {
				v, err := defaultValue(ft, meta.Default)
				if err != nil {
					return nilEnvError(f.Name, err)
				}
				if list, ok := v.([]any); ok {
					parts := make([]string, len(list))
					needsJSON := false
					for i, item := range list {
						parts[i] = fmt.Sprint(item)
						needsJSON = needsJSON || parts[i] == "" || strings.Contains(parts[i], ",") || strings.TrimSpace(parts[i]) != parts[i]
					}
					value = strings.Join(parts, ",")
					if needsJSON || strings.HasPrefix(value, "[") {
						encoded, err := json.Marshal(list)
						if err != nil {
							return nilEnvError(f.Name, err)
						}
						value = string(encoded)
					}
				} else {
					value = fmt.Sprint(v)
				}
			}
			if strings.ContainsAny(value, " \t\r\n#\"'\\") {
				value = strconv.Quote(value)
			}
			fmt.Fprintf(&b, "%s%s=%s\n", prefix, strings.ToUpper(strings.Join(keys, sep)), value)
		}
		return nil
	}
	if err := walk(t, nil); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
func nilEnvError(name string, err error) error {
	return fmt.Errorf("schema: %s default: %w", name, err)
}
