package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// builtin.go consolidates the YAML, JSON, and TOML decoder implementations. The registry init() in
// registry.go registers all three codecs.

type yamlDecoder struct{}

func (yamlDecoder) Decode(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	raw, err := decodeSingleYAMLDocument(data)
	if err != nil {
		return nil, err
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

// decodeSingleYAMLDocument decodes the one document data is allowed to hold. A nil result means
// the input parsed to no document (empty, comments only, or an explicit null); callers map that to
// their own empty value.
func decodeSingleYAMLDocument(data []byte) (any, error) {
	var raw any
	if !hasYAMLDocumentBoundary(data) {
		// Fast path: without a boundary line the stream cannot hold a second
		// document, so skip the decoder and the drain below. This keeps the
		// per-layer reload cost at one Unmarshal for the overwhelmingly
		// common single-document file.
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("yaml: %w", err)
		}
		return raw, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if err := rejectExtraYAMLDocuments(dec); err != nil {
		return nil, err
	}
	return raw, nil
}

var (
	yamlDocStart = []byte("---")
	yamlDocEnd   = []byte("...")
	yamlNLStart  = []byte("\n---")
	yamlNLEnd    = []byte("\n...")
)

// hasYAMLDocumentBoundary reports whether data contains a line opening with
// "---" or "...". A second YAML document always starts with a "---" line, so
// input without one holds at most a single document.
//
// The check is deliberately one-sided. Over-reporting — a "---" line inside a
// multi-line quoted scalar or a block scalar — only costs the slower strict
// path, which still finds exactly one document and succeeds. Under-reporting
// would let a real second document through and resurrect the silent
// truncation this guards against, so every candidate line must match here.
// "\r\n" endings are covered: the "\n---" probe matches the byte after the
// carriage return.
func hasYAMLDocumentBoundary(data []byte) bool {
	if bytes.HasPrefix(data, yamlDocStart) || bytes.HasPrefix(data, yamlDocEnd) {
		return true
	}
	return bytes.Contains(data, yamlNLStart) || bytes.Contains(data, yamlNLEnd)
}

// rejectExtraYAMLDocuments drains dec after the first document and fails when
// any further document carries content. Silently keeping only the first
// document loses configuration with no diagnostic, so this mirrors the
// jsonDecoder contract.
//
// Empty trailing documents are accepted. A stream ending in "---", a trailing
// separator followed by blank lines, or one followed only by a comment all
// decode to a nil document that carries nothing; templating tools emit those
// routinely and rejecting them would break working input without preventing
// any loss. The drain continues past them so content hiding behind an empty
// document is still caught.
func rejectExtraYAMLDocuments(dec *yaml.Decoder) error {
	for {
		var extra any
		switch err := dec.Decode(&extra); {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return fmt.Errorf("yaml: trailing content: %w", err)
		case extra != nil:
			return fmt.Errorf("yaml: multiple documents")
		}
	}
}

// normalize recursively converts map[any]any sub-trees (possible with yaml.v3 anchor references)
// into map[string]any for consistent merger input. Keep the handled type set in sync with
// confmap.DeepClone (the clone boundary): values this collapses to plain map[string]any/[]any are
// the only shapes the downstream walkers — and that clone — recurse.
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
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("json: multiple documents")
		}
		return nil, fmt.Errorf("json: trailing content: %w", err)
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

// tomlDecoder parses TOML bytes into the canonical map[string]any intermediate representation.
// Arrays-of-tables from BurntSushi/toml are folded into []any via normalizeValue for consistent
// deep-merge.
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
