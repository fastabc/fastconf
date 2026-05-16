package decoder

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// builtin.go consolidates the YAML, JSON, and TOML decoder implementations.
// The registry init() in registry.go registers all three codecs.

type yamlDecoder struct{}

func (yamlDecoder) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("yaml: top-level must be a mapping, got %T", raw)
	}
	return normalize(m), nil
}

// normalize recursively converts map[any]any sub-trees (possible with
// yaml.v3 anchor references) into map[string]any for consistent merger input.
func normalize(in map[string]any) map[string]any {
	for k, v := range in {
		in[k] = normalizeValue(v)
	}
	return in
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normalize(t)
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[fmt.Sprint(k)] = normalizeValue(vv)
		}
		return out
	case []map[string]any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = normalize(m)
		}
		return out
	case []any:
		for i := range t {
			t[i] = normalizeValue(t[i])
		}
		return t
	default:
		return v
	}
}

type jsonDecoder struct{}

func (jsonDecoder) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("json: top-level must be an object, got %T", raw)
	}
	return m, nil
}

// tomlDecoder parses TOML bytes into the canonical map[string]any
// intermediate representation. Arrays-of-tables from BurntSushi/toml
// are folded into []any via normalizeValue for consistent deep-merge.
type tomlDecoder struct{}

func (tomlDecoder) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := toml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("toml: %w", err)
	}
	return normalize(out), nil
}
