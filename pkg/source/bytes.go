// Package source provides built-in contracts.Source implementations
// for the koanf-style WithSource(file/http/bytes, parser) call shape.
//
// A Source is a byte-stream layer: Read returns (bytes, contentType,
// revision). The framework pairs it with a Parser (see pkg/parser) via
// Bind to obtain a Provider that participates in the reload pipeline.
// Already-structured sources (env, cli, KV-with-one-key-per-setting)
// continue to use the Provider contract directly — they have no need
// for a Parser.
package source

import (
	"context"
	"crypto/sha1"
	"encoding/hex"

	"github.com/fastabc/fastconf/contracts"
)

// BytesSource is an in-memory byte buffer surfaced as a Source. Use it
// for tests, examples and bootstrap code that wants to inject inline
// configuration without writing a temporary file.
//
// The contentType doubles as the parser hint: when WithSource is
// called with a nil Parser, the parser registry (pkg/parser) is
// consulted with this string. Either bare extensions ("yaml"),
// dotted forms (".yaml") or MIME types ("application/yaml") work.
type BytesSource struct {
	name        string
	contentType string
	data        []byte
	priority    int
	rev         string
}

// NewBytes constructs a BytesSource. The default priority is 9000
// (above file layers, below CLI), matching the legacy bytes-provider
// position in the merge order. Override via WithPriority.
func NewBytes(name, contentType string, data []byte) *BytesSource {
	sum := sha1.Sum(data)
	return &BytesSource{
		name:        name,
		contentType: contentType,
		data:        data,
		priority:    9000,
		rev:         hex.EncodeToString(sum[:8]),
	}
}

// WithPriority returns b with priority overridden.
func (b *BytesSource) WithPriority(p int) *BytesSource { b.priority = p; return b }

// Name implements contracts.Source.
func (b *BytesSource) Name() string { return b.name }

// Priority implements contracts.Source.
func (b *BytesSource) Priority() int { return b.priority }

// Read implements contracts.Source.
func (b *BytesSource) Read(_ context.Context) ([]byte, string, string, error) {
	return b.data, b.contentType, b.rev, nil
}

// Watch implements contracts.Source. In-memory bytes never change.
func (b *BytesSource) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}
