package manager

import "context"

func runPolicy[T any](ctx context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if len(m.opts.Policies) == 0 {
		return nil
	}
	if pc.dryRun {
		// Plan mode: do not block the swap (there is no swap), but capture all
		// findings so the caller can surface them in PlanResult.Policies.
		polErr, warns := m.evaluatePolicies(ctx, pc.target, pc.reason)
		pc.policyViolations = append(pc.policyViolations, warns...)
		if polErr != nil {
			pc.policyViolations = append(pc.policyViolations, polErr.Violations...)
		}
		return nil
	}
	polErr, warns := m.evaluatePolicies(ctx, pc.target, pc.reason)
	for _, w := range warns {
		m.opts.Log.Warn().
			Str("rule", w.Rule).
			Str("path", w.Path).
			Str("msg", w.Message).
			Msg("fastconf policy warning")
	}
	if polErr != nil {
		return polErr
	}
	return nil
}
