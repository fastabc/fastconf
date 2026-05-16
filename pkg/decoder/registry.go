package decoder

import (
	"strings"
	"sync"

	"github.com/fastabc/fastconf/contracts"
)

// registry is the process-global Codec table. It is populated at init
// time with the built-in yaml/json codecs and may be extended at runtime
// via fastconf.RegisterCodec for third-party codecs (toml, hcl, json5, ...).
var (
	registry sync.Map // map[string]contracts.Codec, keys lowercased
	extMap   sync.Map // map[string]string, file extension → codec name
)

// Register installs c under the given name (case-insensitive). It is safe
// for concurrent use. Registering nil panics; that signals a bug at init
// time and we prefer to fail loudly rather than silently dropping the
// codec on first use. Re-registering an existing name overwrites it,
// which makes test helpers and feature toggles ergonomic.
func Register(name string, c contracts.Codec) {
	if c == nil {
		panic("decoder.Register: nil codec for " + name)
	}
	registry.Store(strings.ToLower(name), c)
}

// RegisterExt maps a file extension (with or without leading dot, case
// insensitive) to a codec name. It does NOT register the codec itself —
// the caller should also call Register if the codec is custom.
func RegisterExt(ext, codec string) {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	extMap.Store(ext, strings.ToLower(codec))
}

// Lookup returns the codec for name (case-insensitive) along with an
// "exists" flag. Used by tests to assert registration ordering.
func Lookup(name string) (contracts.Codec, bool) {
	v, ok := registry.Load(strings.ToLower(name))
	if !ok {
		return nil, false
	}
	return v.(contracts.Codec), true
}

// LookupExt returns the codec name for a given extension, or "" if the
// extension is unknown.
func LookupExt(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if v, ok := extMap.Load(ext); ok {
		return v.(string)
	}
	return ""
}

func init() {
	Register("yaml", yamlDecoder{})
	Register("yml", yamlDecoder{})
	Register("json", jsonDecoder{})
	Register("toml", tomlDecoder{})
	RegisterExt("yaml", "yaml")
	RegisterExt("yml", "yaml")
	RegisterExt("json", "json")
	RegisterExt("toml", "toml")
}
