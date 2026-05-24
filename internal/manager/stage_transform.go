package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
)

func runTransform[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	for _, tr := range m.opts.Transformers {
		if err := tr.Transform(pc.merged); err != nil {
			return fmt.Errorf("%w: %s: %w", fcerr.ErrTransform, tr.Name(), err)
		}
	}
	return nil
}
