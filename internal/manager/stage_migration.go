package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
)

func runMigration[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.opts.MigrationRun == nil {
		return nil
	}
	if err := m.opts.MigrationRun.Migrate(pc.merged); err != nil {
		return fmt.Errorf("%w: migration: %w", fcerr.ErrTransform, err)
	}
	return nil
}
