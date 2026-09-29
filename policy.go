package fastconf

import (
	"context"

	"github.com/fastabc/fastconf/policy"
)

func (m *Manager[T]) evaluatePolicies(ctx context.Context, cfg *T, reason string) (*PolicyError, []policy.Violation) {
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
	return &PolicyError{Violations: errs}, warns
}

// anyPolicy is the type-erased policy contract used by WithPolicy.
type anyPolicy interface {
	Name() string
	EvaluateAny(ctx context.Context, cfg any, reason, tenant string) ([]policy.Violation, error)
}

// adaptPolicy erases a policy type, reporting a violation if evaluation receives the wrong type.
func adaptPolicy[T any](p policy.Policy[T]) anyPolicy {
	return policyAdapter[T]{p: p}
}

type policyAdapter[T any] struct{ p policy.Policy[T] }

func (a policyAdapter[T]) Name() string { return a.p.Name() }
func (a policyAdapter[T]) EvaluateAny(ctx context.Context, cfg any, reason, tenant string) ([]policy.Violation, error) {
	c, ok := cfg.(*T)
	if !ok {
		return []policy.Violation{{
			Path:     "",
			Message:  "policy: type mismatch (framework bug)",
			Severity: policy.SeverityError,
			Rule:     a.p.Name(),
		}}, nil
	}
	return a.p.Evaluate(ctx, policy.Input[T]{Config: c, Reason: reason, Tenant: tenant})
}
