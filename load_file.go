package fastconf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

// LoadFile reads one required file (at most 4 MiB), using its extension's codec
// and JSON field tags. Unknown fields fail; absent-key defaults, typed hooks,
// fc defaults, Defaulter and field validation run normally. It skips discovery, profiles and environment
// overrides, starts no workers and returns a caller-owned *T. Use Load for options
// or diagnostics and New for runtime updates.
func LoadFile[T any](path string) (*T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	defer func() { _ = file.Close() }()
	ext := filepath.Ext(path)
	if _, ok := codec.ByContentType(ext); !ok {
		return nil, fmt.Errorf("%w: %s: %w: extension %q", ErrDecode, path, codec.ErrUnknownCodec, ext)
	}
	data, err := contracts.ReadAllMax(file, contracts.DefaultMaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrProvider, path, err)
	}
	state, err := Load[T](context.Background(),
		func(o *options) { o.SkipDiscovery = true },
		WithProvider(source.NewBytes(path, ext, data)),
		WithUnknownFields(UnknownError),
	)
	if err != nil {
		return nil, err
	}
	return state.Value(), nil
}
