package codec

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fastabc/fastconf/contracts"
)

// registry is the process-global Codec table. It is populated at init time with the built-in
// yaml/json codecs and may be extended at runtime via Register for third-party codecs (hcl, json5,
// ...).
var (
	registry   sync.Map // map[string]contracts.Codec, keys lowercased
	extMap     sync.Map // map[string]string, file extension → codec name
	generation atomic.Uint64
)

// Generation advances after each codec registration. Caches of decoded documents sample it before
// looking up a decoder; a concurrent registration then invalidates that sample on the next reload,
// even when it replaces a codec under the same name.
func Generation() uint64 { return generation.Load() }

// Register installs c under the given name (case-insensitive). It is safe for concurrent use.
// Registering nil panics; that signals a bug at init time and we prefer to fail loudly rather than
// silently dropping the codec on first use. Re-registering an existing name overwrites it, which
// makes test helpers and feature toggles ergonomic.
func Register(name string, c contracts.Codec) {
	if c == nil {
		panic("decoder.Register: nil codec for " + name)
	}
	registry.Store(strings.ToLower(name), c)
	generation.Add(1)
}

// RegisterExt maps a file extension (with or without leading dot, case insensitive) to a codec
// name. It does NOT register the codec itself — the caller should also call Register if the codec
// is custom.
func RegisterExt(ext, codec string) {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	extMap.Store(ext, strings.ToLower(codec))
}

// Lookup returns the registered codec for name (case-insensitive). The boolean
// reports whether the codec exists. It is safe for concurrent use.
func Lookup(name string) (contracts.Codec, bool) {
	v, ok := registry.Load(strings.ToLower(name))
	if !ok {
		return nil, false
	}
	return v.(contracts.Codec), true
}

// LookupExt returns the codec name for a given extension, or "" if the extension is unknown.
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

// ByContentType returns the codec for a MIME type or file-extension hint ("application/yaml",
// "text/json", ".toml", "yml"). Parameters such as "; charset=utf-8" are ignored. Unknown hints
// report false.
func ByContentType(ct string) (contracts.Codec, bool) {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if i := strings.LastIndexByte(ct, '/'); i >= 0 {
		ct = ct[i+1:]
		ct = strings.TrimPrefix(ct, "x-")
	}
	if name := LookupExt(ct); name != "" {
		return Lookup(name)
	}
	return nil, false
}
