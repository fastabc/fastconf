package fastconf

import (
	"context"
	"time"

	"github.com/fastabc/fastconf/policy"
)

// PlanResult describes the outcome of Manager.Plan.
type PlanResult[T any] struct {
	Proposed   *State[T]
	Diff       []DiffEntry
	Validators []ValidatorReport
	// Policies holds all policy findings (warnings and errors alike) gathered
	// during the dry-run. Findings with SeverityError would have aborted a real
	// reload; here they are captured for inspection instead.
	Policies []policy.Violation
}

// ValidatorReport is one row in PlanResult.Validators.
type ValidatorReport struct {
	Name string
	Err  error
}

// PlanOption tunes one Manager.Plan call.
type PlanOption func(*planConfig)

type planConfig struct {
	hostname string
}

// WithPlanHostname pins the hostname used to resolve multi-axis overlay
// axes that rely on Axis.FromHostname. Use it from preview tools or
// PR bots running on CI runners so the produced diff reflects the target
// environment instead of "ci-runner-7".
func WithPlanHostname(host string) PlanOption {
	return func(c *planConfig) { c.hostname = host }
}

// Plan previews the pipeline without publication. It queues on the same writer as
// Reload, so user hooks remain serialized. Hooks may still have external effects;
// a failed preview also reaches Errors. Inspect both Validators and Policies.
func (m *Manager[T]) Plan(ctx context.Context, opts ...PlanOption) (*PlanResult[T], error) {
	if m == nil {
		return nil, ErrClosed
	}
	var cfg planConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	var res *PlanResult[T]
	err := m.submitAndWait(ctx, reloadRequest{
		ctx:    ctx,
		reason: "plan",
		run: func(pipeCtx context.Context) error {
			r, err := m.runPlan(pipeCtx, cfg.hostname)
			if err != nil {
				return err
			}
			res = r
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// runPlan performs the dry-run pipeline on the reload goroutine. It must
// only be called from reloadLoop via Plan's submitAndWait so the single-writer
// invariant holds.
func (m *Manager[T]) runPlan(ctx context.Context, hostname string) (*PlanResult[T], error) {
	assembly, err := m.assemble(ctx, hostname)
	if err != nil {
		return nil, err
	}
	pipeline, hash, err := m.runPipeline(ctx, assembly, "plan", true)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	proposed := m.newState(pipeline, hash, m.gen.Load(), ReloadCause{Reason: "plan", At: now, Tenant: m.tenant})

	var diff []DiffEntry
	if cur := m.state.Load(); cur != nil {
		diff = diagnosticDiff(cur, proposed)
	}
	return &PlanResult[T]{
		Proposed:   proposed,
		Diff:       diff,
		Validators: pipeline.reports,
		Policies:   pipeline.policyViolations,
	}, nil
}
