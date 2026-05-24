package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
	ipipeline "github.com/fastabc/fastconf/internal/pipeline"
)

func runFieldMetaCheck[T any](_ context.Context, _ *M[T], pc *pipelineCtx[T]) error {
	if pc.target == nil {
		return nil
	}
	violations := ipipeline.CheckFieldMeta(pc.target)
	if len(violations) == 0 {
		return nil
	}
	if pc.dryRun {
		for _, v := range violations {
			pc.reports = append(pc.reports, ValidatorReport{
				Name: "fastconf:field-meta",
				Err:  fmt.Errorf("%w: %s", fcerr.ErrValidator, v.Msg),
			})
		}
		return nil
	}
	// Collect all violations and join them so callers can inspect each one.
	errs := make([]error, len(violations))
	for i, v := range violations {
		errs[i] = fmt.Errorf("%w: %s", fcerr.ErrValidator, v.Msg)
	}
	return errors.Join(errs...)
}
