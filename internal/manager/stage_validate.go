package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
)

func runValidate[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if pc.dryRun {
		// Plan semantics: collect every result, never abort.
		pc.reports = make([]ValidatorReport, 0, len(m.opts.Validators))
		for i, v := range m.opts.Validators {
			pc.reports = append(pc.reports, ValidatorReport{
				Name: fmt.Sprintf("validator[%d]", i),
				Err:  v.Fn(pc.target),
			})
		}
		return nil
	}
	for _, v := range m.opts.Validators {
		if err := v.Fn(pc.target); err != nil {
			return fmt.Errorf("%w: %v", fcerr.ErrValidator, err)
		}
	}
	return nil
}
