package fastconf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
)

// LoadFile reads one required file (at most 4 MiB), using its extension's codec
// and JSON field tags. Unknown fields fail; absent-key defaults, typed hooks,
// Defaulter and field validation run normally. It skips discovery, profiles and
// environment overrides, starts no workers and returns a caller-owned *T. Use Load
// for options or diagnostics and New for runtime updates.
//
// Errors classify as follows: a missing or unreadable file wraps ErrProvider (an
// oversized file also wraps ErrTooLarge); an unknown extension, a syntax error, a
// value that does not decode into T or an unknown key wraps ErrDecode; field and
// validator checks wrap ErrInvalid. Invalid default declarations fail before
// reading the file. Decoder and default hook panics return ErrPanic.
func LoadFile[T any](path string) (_ *T, err error) {
	ctx := context.Background()
	m, err := newManager[T](ctx, []Option{
		func(o *options) { o.SkipDiscovery = true },
		WithUnknownFields(UnknownError),
	})
	if err != nil {
		return nil, err
	}
	defer m.lifetimeCancel()
	defer recoverPanic(ctx, m.opts.Logger, "assemble", &err)

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	defer func() { _ = file.Close() }()
	ext := filepath.Ext(path)
	dec, ok := codec.ByContentType(ext)
	if !ok {
		return nil, fmt.Errorf("%w: %s: %w: extension %q", ErrDecode, path, codec.ErrUnknownCodec, ext)
	}
	data, err := contracts.ReadAllMax(file, contracts.DefaultMaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrProvider, path, err)
	}
	tree := map[string]any{}
	if len(data) > 0 {
		decoded, err := dec.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrDecode, path, err)
		}
		if decoded != nil {
			tree = decoded
		}
	}
	m.opts.addProvider(decodedFile{name: path, tree: tree})
	state, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Value(), nil
}

// decodedFile is LoadFile's already-decoded document. Decoding before Load keeps
// syntax errors classified as ErrDecode, as in directory discovery, rather than
// as provider failures.
type decodedFile struct {
	name string
	tree map[string]any
}

func (f decodedFile) Name() string { return f.name }

func (f decodedFile) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: f.tree}, nil
}
