// Package decoder turns bytes of various encodings (yaml/json/...) into
// a uniform map[string]any intermediate representation. That
// representation lives only inside the reload pipeline and never
// appears in the public API.
//
// Decoders are registered through a process-wide registry (see
// registry.go). The built-in yaml/yml/json codecs register themselves
// in init(); external codecs (toml/hcl/json5/...) plug in from outside
// the repo via fastconf.RegisterCodec.
package decoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/contracts"
)

// Decoder turns a byte stream into a generic map.
//
// Decoder is an alias for the public contracts.Codec interface, so any
// implementation of contracts.Codec can be fed directly into this
// package's registry.
type Decoder = contracts.Codec

// ErrUnknownCodec means the codec name was not registered.
var ErrUnknownCodec = errors.New("decoder: unknown codec")

// For returns the Decoder for a codec name. Codec lookup is case
// insensitive. Returns ErrUnknownCodec when the codec is not
// registered.
func For(codec string) (Decoder, error) {
	if c, ok := Lookup(codec); ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
}

// CodecFromExt infers a codec name from a file extension (with or without
// the leading dot). Returns empty string when the extension is unrecognised.
// Queries only codecs registered via RegisterCodec/RegisterCodecExt.
func CodecFromExt(ext string) string {
	return LookupExt(ext)
}

// DecodeAny decodes data as either an object or an array (used for RFC 6902
// patch layers whose top-level node is an array).
// Returns either map[string]any or []any. Returns nil on empty input.
func DecodeAny(codec string, data []byte) (any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	switch strings.ToLower(codec) {
	case "yaml", "yml":
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("yaml: %w", err)
		}
		return normalizeValue(raw), nil
	case "json":
		var raw any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("json: %w", err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
	}
}
