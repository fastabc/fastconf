package manager

import (
	"context"
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
	return fmt.Errorf("%w: %s", fcerr.ErrValidator, violations[0].Msg)
}
