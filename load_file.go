package fastconf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
	"github.com/fastabc/fastconf/providers/source"
)

// LoadFile loads exactly one required file and returns its typed configuration.
// The extension selects the input codec (YAML, JSON, TOML or INI by default);
// json struct tags name fields regardless of the input format. Unknown fields
// are rejected, and typed hooks, fc tag defaults, Defaulter and fc validation
// run through the standard loading pipeline.
//
// LoadFile does not discover conf.d, profiles or environment overrides, and
// starts no background workers. The file is limited to 4 MiB. The caller owns
// the returned value; there is no State or Manager to close. Use Load when
// options, custom validation or diagnostic snapshots are needed, and New for
// runtime updates.
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
	data, err := providerutil.ReadAllMax(file, contracts.DefaultMaxBodyBytes)
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
