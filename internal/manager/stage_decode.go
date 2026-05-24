package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
	iopts "github.com/fastabc/fastconf/internal/options"
)

func runDecode[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.opts.RawMapHook != nil {
		m.opts.RawMapHook(pc.merged)
	}
	pc.target = new(T)
	b, err := decodeInto(pc.merged, pc.target, m.opts.CodecBridge)
	if err != nil {
		return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
	}
	pc.mergedJSON = b
	if m.opts.StructDefaults != nil {
		if err := m.opts.StructDefaults(pc.target); err != nil {
			return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
		}
	}
	// iopts.Defaulter interface: auto-call Defaults() if *T implements it.
	if d, ok := any(pc.target).(iopts.Defaulter); ok {
		d.Defaults()
	}
	// WithDefaults: explicit callback for types that cannot implement
	// iopts.Defaulter.
	if m.opts.DefaulterFunc != nil {
		m.opts.DefaulterFunc(pc.target)
	}
	return nil
}
