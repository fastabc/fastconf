// Package parser exposes the koanf-style Parser slot used at the
// Manager call site (WithSource(file.New(path), yaml.Parser())).
//
// A Parser is contracts.Codec plus a list of canonical content-types
// ("yaml", ".yaml", ".yml", "application/yaml"). The built-in yaml /
// json / toml parsers are registered in this package's init so that
// Bind can auto-select a parser from a Source.Read content-type hint
// when the caller passes nil.
//
// Parsers are thin wrappers over pkg/decoder Codecs; the registry here
// is a content-type index on top of that flat map.
package parser

import (
	"strings"
	"sync"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
)

// parser binds a decoder.Codec with a fixed set of content-types.
type parser struct {
	codec        contracts.Codec
	contentTypes []string
}

func (p parser) Decode(data []byte) (map[string]any, error) { return p.codec.Decode(data) }
func (p parser) ContentTypes() []string                     { return p.contentTypes }

// New constructs a Parser from an existing Codec plus the
// content-types this Parser claims. The content-types are stored
// case-folded; lookup is case-insensitive.
func New(c contracts.Codec, contentTypes ...string) contracts.Parser {
	folded := make([]string, len(contentTypes))
	for i, ct := range contentTypes {
		folded[i] = strings.ToLower(ct)
	}
	return parser{codec: c, contentTypes: folded}
}

// YAML returns the built-in YAML parser. It claims "yaml", ".yaml",
// ".yml", "application/yaml", "application/x-yaml", "text/yaml".
func YAML() contracts.Parser { return mustParser("yaml") }

// JSON returns the built-in JSON parser. It claims "json", ".json",
// "application/json", "text/json".
func JSON() contracts.Parser { return mustParser("json") }

// TOML returns the built-in TOML parser. It claims "toml", ".toml",
// "application/toml", "text/toml".
func TOML() contracts.Parser { return mustParser("toml") }

// ---------------------------------------------------------------------
// Registry: contentType → Parser
// ---------------------------------------------------------------------

var registry sync.Map // map[string]contracts.Parser, keys lowercased

// Register installs p under every content-type it claims. Subsequent
// calls override prior registrations for the same content-type, which
// makes test helpers and feature toggles ergonomic.
func Register(p contracts.Parser) {
	if p == nil {
		panic("parser.Register: nil parser")
	}
	for _, ct := range p.ContentTypes() {
		registry.Store(strings.ToLower(ct), p)
	}
}

// Lookup returns the Parser registered for the given content-type,
// or (nil, false) if none matches. Lookup is case-insensitive and
// accepts both the bare extension ("yaml"), the dotted form (".yaml"),
// and the MIME type ("application/yaml"). The empty string is never a
// hit.
func Lookup(contentType string) (contracts.Parser, bool) {
	if contentType == "" {
		return nil, false
	}
	key := strings.ToLower(contentType)
	if v, ok := registry.Load(key); ok {
		return v.(contracts.Parser), true
	}
	// Accept "yaml" when caller passed ".yaml" or vice-versa.
	if strings.HasPrefix(key, ".") {
		if v, ok := registry.Load(key[1:]); ok {
			return v.(contracts.Parser), true
		}
	} else {
		if v, ok := registry.Load("." + key); ok {
			return v.(contracts.Parser), true
		}
	}
	return nil, false
}

func mustParser(name string) contracts.Parser {
	for _, ct := range contentTypesFor(name) {
		if p, ok := Lookup(ct); ok {
			return p
		}
	}
	panic("parser: built-in parser not registered: " + name)
}

func contentTypesFor(name string) []string {
	switch strings.ToLower(name) {
	case "yaml":
		return []string{"yaml", ".yaml", ".yml", "application/yaml", "application/x-yaml", "text/yaml"}
	case "json":
		return []string{"json", ".json", "application/json", "text/json"}
	case "toml":
		return []string{"toml", ".toml", "application/toml", "text/toml"}
	}
	return nil
}

func init() {
	yamlCodec, _ := decoder.Lookup("yaml")
	jsonCodec, _ := decoder.Lookup("json")
	tomlCodec, _ := decoder.Lookup("toml")
	if yamlCodec != nil {
		Register(New(yamlCodec, contentTypesFor("yaml")...))
	}
	if jsonCodec != nil {
		Register(New(jsonCodec, contentTypesFor("json")...))
	}
	if tomlCodec != nil {
		Register(New(tomlCodec, contentTypesFor("toml")...))
	}
}
