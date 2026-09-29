package fastconf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/fastabc/fastconf/internal/secret"
	"gopkg.in/yaml.v3"
)

// Format names a configuration encoding. State.Dump accepts all three;
// WithDecoder accepts JSON and YAML.
type Format uint8

const (
	// YAML: Dump emits deterministic YAML with map keys sorted
	// lexicographically; WithDecoder decodes *T through yaml struct tags.
	YAML Format = iota
	// JSON: Dump emits indented JSON; WithDecoder (the default) decodes *T
	// through encoding/json and json struct tags.
	JSON
	// TOML: Dump emits canonical TOML via BurntSushi/toml.
	TOML
)

// String reports the format's lowercase token.
func (f Format) String() string {
	switch f {
	case YAML:
		return "yaml"
	case JSON:
		return "json"
	case TOML:
		return "toml"
	default:
		return fmt.Sprintf("format(%d)", uint8(f))
	}
}

// dumpState serializes the snapshot to the requested format. When redactor is
// non-nil, secret-tagged paths in *T are masked before serialization;
// when nil, the raw JSON tree is emitted. Key ordering is deterministic
// across formats so two snapshots whose values match produce byte-identical
// output.
func dumpState[T any](s *State[T], format Format, redactor secret.Redactor) ([]byte, error) {
	tree, err := dumpTree(s, redactor)
	if err != nil {
		return nil, err
	}
	switch format {
	case JSON:
		return json.MarshalIndent(tree, "", "  ")
	case TOML:
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(tomlNumbers(tree)); err != nil {
			return nil, fmt.Errorf("toml: %w", err)
		}
		return buf.Bytes(), nil
	case YAML:
		node, err := orderedYAMLNode(tree)
		if err != nil {
			return nil, err
		}
		return yaml.Marshal(node)
	default:
		// Unknown Format values are not covered by the iota enum;
		// this branch is defensive and should never be reached in
		// well-formed callers.
		return nil, fmt.Errorf("fastconf: unknown Format %s", format)
	}
}

// dumpTree resolves the map view used by Dump. Nil snapshots fall through
// to an empty map so callers always get well-formed output.
func dumpTree[T any](s *State[T], redactor secret.Redactor) (map[string]any, error) {
	if s == nil {
		return map[string]any{}, nil
	}
	tree, err := s.treeChecked(redactor)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		return map[string]any{}, nil
	}
	return tree, nil
}

type tomlNumber json.Number

func (n tomlNumber) MarshalTOML() ([]byte, error) {
	value := json.Number(n)
	if strings.ContainsAny(string(n), ".eE") {
		if _, err := value.Float64(); err != nil {
			return nil, err
		}
	} else if _, err := value.Int64(); err != nil {
		return nil, err
	}
	return []byte(n), nil
}

func tomlNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		return tomlNumber(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = tomlNumbers(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = tomlNumbers(item)
		}
		return out
	default:
		return value
	}
}

// orderedYAMLNode preserves JSON numbers and null containers while producing
// stable, lexicographically ordered maps for both the YAML bridge and Dump.
func orderedYAMLNode(v any) (*yaml.Node, error) {
	switch t := v.(type) {
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}, nil
	case map[string]any:
		if t == nil {
			return orderedYAMLNode(nil)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		node := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			child, err := orderedYAMLNode(t[k])
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				child,
			)
		}
		return node, nil
	case []any:
		if t == nil {
			return orderedYAMLNode(nil)
		}
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range t {
			child, err := orderedYAMLNode(e)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		return node, nil
	default:
		n := &yaml.Node{}
		err := n.Encode(v)
		return n, err
	}
}
