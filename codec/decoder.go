// Package codec decodes source documents into public map trees and applies typed
// hooks before struct decoding. WithDecodeFormat selects that separate struct bridge.
// YAML, JSON, TOML and INI register at init; Register accepts external codecs.
package codec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/fastabc/fastconf/contracts"
)

// ErrUnknownCodec means the codec name was not registered.
var ErrUnknownCodec = errors.New("decoder: unknown codec")

// For returns the document decoder for a codec name. Codec lookup is case insensitive. Returns
// ErrUnknownCodec when the codec is not registered.
func For(codec string) (contracts.Decoder, error) {
	if c, ok := Lookup(codec); ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
}

// DecodeAny decodes data as either an object or an array (used for RFC 6902 patch layers whose
// top-level node is an array). Returns either map[string]any or []any. Returns nil on empty input.
// Only YAML and JSON are supported: patch documents are always JSON or YAML.
// TOML and INI are document codecs and are intentionally excluded.
func DecodeAny(codec string, data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	switch strings.ToLower(codec) {
	case "yaml", "yml":
		raw, err := decodeSingleYAMLDocument(data)
		if err != nil {
			return nil, err
		}
		return normalizeValue(raw), nil
	case "json":
		return decodeSingleJSONDocument(data)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
	}
}
