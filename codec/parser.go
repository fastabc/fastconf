package codec

import (
	"strings"
	"sync"

	"github.com/fastabc/fastconf/contracts"
)

type parser struct {
	codec        contracts.Codec
	contentTypes []string
}

func (p parser) Decode(data []byte) (map[string]any, error) { return p.codec.Decode(data) }
func (p parser) ContentTypes() []string                     { return p.contentTypes }

// NewParser constructs a Parser from an existing Codec plus the content-types
// this Parser claims. The content-types are stored case-folded; lookup is
// case-insensitive.
func NewParser(c contracts.Codec, contentTypes ...string) contracts.Parser {
	folded := make([]string, len(contentTypes))
	for i, ct := range contentTypes {
		folded[i] = strings.ToLower(ct)
	}
	return parser{codec: c, contentTypes: folded}
}

func YAMLParser() contracts.Parser { return mustParser("yaml") }
func JSONParser() contracts.Parser { return mustParser("json") }
func TOMLParser() contracts.Parser { return mustParser("toml") }

var parserRegistry sync.Map // map[string]contracts.Parser, keys lowercased

// RegisterParser installs p under every content-type it claims. Subsequent
// calls override prior registrations for the same content-type.
func RegisterParser(p contracts.Parser) {
	if p == nil {
		panic("codec.RegisterParser: nil parser")
	}
	for _, ct := range p.ContentTypes() {
		parserRegistry.Store(strings.ToLower(ct), p)
	}
}

// LookupParser returns the Parser registered for the given content-type.
func LookupParser(contentType string) (contracts.Parser, bool) {
	if contentType == "" {
		return nil, false
	}
	key := strings.ToLower(contentType)
	if v, ok := parserRegistry.Load(key); ok {
		return v.(contracts.Parser), true
	}
	if strings.HasPrefix(key, ".") {
		if v, ok := parserRegistry.Load(key[1:]); ok {
			return v.(contracts.Parser), true
		}
	} else {
		if v, ok := parserRegistry.Load("." + key); ok {
			return v.(contracts.Parser), true
		}
	}
	return nil, false
}

func mustParser(name string) contracts.Parser {
	for _, ct := range contentTypesFor(name) {
		if p, ok := LookupParser(ct); ok {
			return p
		}
	}
	panic("codec: built-in parser not registered: " + name)
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

func registerBuiltInParsers() {
	yamlCodec, _ := Lookup("yaml")
	jsonCodec, _ := Lookup("json")
	tomlCodec, _ := Lookup("toml")
	if yamlCodec != nil {
		RegisterParser(NewParser(yamlCodec, contentTypesFor("yaml")...))
	}
	if jsonCodec != nil {
		RegisterParser(NewParser(jsonCodec, contentTypesFor("json")...))
	}
	if tomlCodec != nil {
		RegisterParser(NewParser(tomlCodec, contentTypesFor("toml")...))
	}
}
