// Package source provides local-file and in-memory document providers. Use providers/http for
// remote documents.
package source

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
)

// BytesSource is an immutable in-memory document implementing contracts.Provider. Use it
// for tests, examples and bootstrap code that wants to inject inline
// configuration without writing a temporary file.
//
// contentType selects the codec: bare extensions ("yaml"), dotted forms
// (".yaml") or MIME types ("application/yaml") all work.
type BytesSource struct {
	name         string
	contentType  string
	data         []byte
	priority     int
	rev          string
	maxBodyBytes int64
}

// NewBytes copies data into a BytesSource. The default priority is contracts.PriorityStatic,
// so env and CLI providers override it. Override via WithPriority.
func NewBytes(name, contentType string, data []byte) *BytesSource {
	data = bytes.Clone(data)
	sum := sha1.Sum(data)
	return &BytesSource{
		name:         name,
		contentType:  contentType,
		data:         data,
		priority:     contracts.PriorityStatic,
		rev:          hex.EncodeToString(sum[:8]),
		maxBodyBytes: contracts.DefaultMaxBodyBytes,
	}
}

// WithPriority returns b with priority overridden.
func (b *BytesSource) WithPriority(p int) *BytesSource { b.priority = p; return b }

// WithMaxBodyBytes limits the in-memory document before it is decoded. Zero restores the 4 MiB
// default.
func (b *BytesSource) WithMaxBodyBytes(n int64) *BytesSource { b.maxBodyBytes = n; return b }

// Name implements contracts.Provider.
func (b *BytesSource) Name() string { return b.name }

// Describe implements contracts.Describer.
func (b *BytesSource) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: b.priority}
}

// Load implements contracts.Provider.
func (b *BytesSource) Load(ctx context.Context) (contracts.Snapshot, error) {
	data, ct, rev, err := b.Read(ctx)
	return decode(b.name, data, ct, rev, err)
}

// Read returns a copy of the raw document, its content-type hint and revision.
func (b *BytesSource) Read(_ context.Context) ([]byte, string, string, error) {
	max := b.maxBodyBytes
	if max <= 0 {
		max = contracts.DefaultMaxBodyBytes
	}
	if int64(len(b.data)) > max {
		return nil, "", "", fmt.Errorf("bytes source: %w: limit=%d bytes", contracts.ErrConfigTooLarge, max)
	}
	return bytes.Clone(b.data), b.contentType, b.rev, nil
}

// Watch implements contracts.Provider. In-memory bytes never change.
func (b *BytesSource) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

// decode turns a Read result into a Snapshot with the codec matching ct. An empty document is an
// empty layer.
func decode(name string, data []byte, ct, rev string, err error) (contracts.Snapshot, error) {
	if err != nil {
		return contracts.Snapshot{}, err
	}
	if len(data) == 0 {
		return contracts.Snapshot{Map: map[string]any{}, Revision: rev}, nil
	}
	c, ok := codec.ByContentType(ct)
	if !ok {
		return contracts.Snapshot{}, fmt.Errorf("source %q: %w: content-type %q", name, codec.ErrUnknownCodec, ct)
	}
	m, err := c.Decode(data)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	return contracts.Snapshot{Map: m, Revision: rev}, nil
}
