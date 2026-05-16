package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
)

// BytesProvider exposes a raw byte slice as a Provider, decoded via the
// registered codec ("yaml" / "json" / "toml"). Use it to inject in-memory
// configuration layers for tests, quickstarts, or programmatic generation
// without writing a temporary file.
type BytesProvider struct {
	name     string
	codec    string
	data     []byte
	priority int
}

// NewBytes wraps a name+codec+data triple as a Provider. The default
// priority is 9000 (the same numeric band the legacy WithBytes injection
// used — above file overlays, below CLI). Override with WithPriority.
func NewBytes(name, codec string, data []byte) *BytesProvider {
	return &BytesProvider{
		name:     name,
		codec:    codec,
		data:     data,
		priority: 9000,
	}
}

// NewSource is the contracts.Source-shaped constructor: equivalent to
// NewBytes(s.Name, s.Codec, s.Data).
func NewSource(s contracts.Source) *BytesProvider {
	return NewBytes(s.Name, s.Codec, s.Data)
}

// WithPriority overrides the default priority.
func (p *BytesProvider) WithPriority(prio int) *BytesProvider { p.priority = prio; return p }

// Name implements Provider. Returns the bare name (the pipeline prepends
// "provider://" when stamping the SourceRef path).
func (p *BytesProvider) Name() string { return p.name }

// Priority implements Provider.
func (p *BytesProvider) Priority() int { return p.priority }

// Load decodes the bytes through the registered codec on every call. For
// test fixtures this is fine; for hot paths cache the decoded map yourself.
func (p *BytesProvider) Load(_ context.Context) (map[string]any, error) {
	dec, err := decoder.For(p.codec)
	if err != nil {
		return nil, err
	}
	return dec.Decode(p.data)
}

// Watch implements Provider. In-memory bytes never change.
func (p *BytesProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}
