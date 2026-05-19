package state

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/internal/secret"
)

// DumpFormat selects the serialization format for [Dump].
type DumpFormat uint8

const (
	// DumpYAML emits deterministic YAML with map keys sorted lexicographically.
	DumpYAML DumpFormat = iota
	// DumpJSON emits indented JSON (two-space indent, sorted keys via map walk).
	DumpJSON
	// DumpTOML emits canonical TOML via BurntSushi/toml.
	DumpTOML
)

// String reports the format's lowercase token.
func (f DumpFormat) String() string {
	switch f {
	case DumpYAML:
		return "yaml"
	case DumpJSON:
		return "json"
	case DumpTOML:
		return "toml"
	default:
		return fmt.Sprintf("dumpformat(%d)", uint8(f))
	}
}

// Dump serializes the snapshot to the requested format. When redactor is
// non-nil, secret-tagged paths in *T are masked before serialization;
// when nil, the raw ValueMap is emitted. Key ordering is deterministic
// across formats so two snapshots whose values match produce byte-identical
// output.
func Dump[T any](s *State[T], format DumpFormat, redactor secret.Redactor) ([]byte, error) {
	tree := dumpTree(s, redactor)
	switch format {
	case DumpJSON:
		return json.MarshalIndent(tree, "", "  ")
	case DumpTOML:
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(tree); err != nil {
			return nil, fmt.Errorf("toml: %w", err)
		}
		return buf.Bytes(), nil
	case DumpYAML:
		return yaml.Marshal(orderedYAMLNode(tree))
	default:
		return nil, fmt.Errorf("fastconf: unknown DumpFormat %s", format)
	}
}

// dumpTree resolves the map view used by Dump. Nil snapshots fall through
// to an empty map so callers always get well-formed output.
func dumpTree[T any](s *State[T], redactor secret.Redactor) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	tree := s.tree(redactor)
	if tree != nil {
		return tree
	}
	// Defensive fall-back: nil Value but populated Introspection.
	if intro := s.Introspect(); intro != nil {
		if t := UnflattenForYAML(intro.Settings()); t != nil {
			return t
		}
	}
	return map[string]any{}
}
