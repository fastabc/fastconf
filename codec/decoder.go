// Package codec decodes source documents into map[string]any trees, also
// exposed by provider snapshots and map transforms. Typed hooks convert
// tree values for target fields; fastconf.WithDecodeFormat separately selects
// the final JSON or YAML bridge into the application's struct.
//
// Decoders are registered through a process-wide registry (see
// registry.go). The built-in yaml/yml/json/toml/ini codecs register themselves
// in init(); external codecs (hcl/json5/...) plug in from outside
// the repo via Register.
package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		// Same contract as the json branch below: a patch file holding more
		// than one content document must fail rather than apply only its
		// first document. decodeSingleYAMLDocument lives in builtin.go.
		raw, err := decodeSingleYAMLDocument(data)
		if err != nil {
			return nil, err
		}
		return normalizeValue(raw), nil
	case "json":
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
		return raw, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
	}
}
