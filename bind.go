package fastconf

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/parser"
)

// ErrParserUnknown is returned by a bound Source/Parser composite when
// no Parser was supplied to Bind and the Source's content-type hint
// did not match any registered Parser. The error is observed at the
// first Load() call, not at Bind time, because content-types may be
// runtime-discovered (e.g. HTTP Content-Type header).
var ErrParserUnknown = errors.New("fastconf: no parser for source content-type")

// Bind composes a byte-stream Source with a Parser into a
// contracts.Provider that the reload pipeline can consume. The
// returned Provider forwards Name/Priority/Watch to the Source and
// runs Source.Read + Parser.Decode on Load.
//
// If parser is nil, the framework attempts to resolve a Parser from
// the parser registry using the content-type hint returned by
// Source.Read. The lookup is deferred to Load() so that Sources whose
// content-type is only known at runtime (HTTP Content-Type response
// header, magic-byte detection, ...) work without ceremony. If
// neither an explicit Parser nor a registry match resolves a Parser
// by Load time, Load returns ErrParserUnknown.
func Bind(src contracts.Source, p contracts.Parser) contracts.Provider {
	return &boundSource{src: src, parser: p}
}

type boundSource struct {
	src    contracts.Source
	parser contracts.Parser
}

func (b *boundSource) Name() string     { return b.src.Name() }
func (b *boundSource) Priority() int    { return b.src.Priority() }
func (b *boundSource) Load(ctx context.Context) (map[string]any, error) {
	data, ct, _, err := b.src.Read(ctx)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	p := b.parser
	if p == nil {
		var ok bool
		p, ok = parser.Lookup(ct)
		if !ok {
			return nil, fmt.Errorf("%w: source %q content-type %q", ErrParserUnknown, b.src.Name(), ct)
		}
	}
	return p.Decode(data)
}

func (b *boundSource) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	return b.src.Watch(ctx)
}
