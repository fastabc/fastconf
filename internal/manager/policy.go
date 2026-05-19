package manager

import (
	"context"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/policy"
)

func (m *M[T]) evaluatePolicies(ctx context.Context, cfg *T, reason string) (*fcerr.PolicyError, []policy.Violation) {
	if len(m.opts.Policies) == 0 {
		return nil, nil
	}
	var errs []policy.Violation
	var warns []policy.Violation
	for _, p := range m.opts.Policies {
		vs, err := p.EvaluateAny(ctx, cfg, reason, m.tenant)
		if err != nil {
			errs = append(errs, policy.Violation{
				Rule:     p.Name(),
				Message:  "evaluation error: " + err.Error(),
				Severity: policy.SeverityError,
			})
			continue
		}
		for _, v := range vs {
			if v.Rule == "" {
				v.Rule = p.Name()
			}
			if v.Severity == policy.SeverityError {
				errs = append(errs, v)
			} else {
				warns = append(warns, v)
			}
		}
	}
	if len(errs) == 0 {
		return nil, warns
	}
	return &fcerr.PolicyError{Violations: errs}, warns
}
