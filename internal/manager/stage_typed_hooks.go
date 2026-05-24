package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// runTypedHooks rewrites leaves of the merged map to forms that the
// JSON decoder can natively assign to *T's fields ("30s" → int64
// nanoseconds for time.Duration, etc). The plan is precomputed once at
// Manager construction so this stage is a tree walk with no reflection.
func runTypedHooks[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.typedHookPlan == nil {
		return nil
	}
	if err := m.typedHookPlan.Apply(pc.merged); err != nil {
		return fmt.Errorf("%w: typed-hook: %w", fcerr.ErrTransform, err)
	}
	return nil
}
