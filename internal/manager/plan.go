package manager

// Plan / dry-run preview. The pipeline is shared with commit but no
// atomic publish happens and the generation is not incremented.

import (
	"context"
	"fmt"
	"time"

	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/policy"
)

// PlanResult describes the outcome of Manager.Plan.
type PlanResult[T any] struct {
	Proposed   *istate.State[T]
	Diff       []istate.DiffEntry
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

// PlanBuilder is the dry-run builder returned by Manager.Plan(). Use the
// With* chain to tune the preview, then call Run(ctx) to execute.
type PlanBuilder[T any] struct {
	m                *M[T]
	hostnameOverride string
}

// Plan opens a dry-run builder. The actual preview executes when Run is
// called; nothing happens beforehand.
//
//	result, err := m.Plan().
//	    WithHostname("prod-eu-1").
//	    Run(ctx)
func (m *M[T]) Plan() *PlanBuilder[T] {
	return &PlanBuilder[T]{m: m}
}

// WithHostname pins the hostname value used to resolve multi-axis
// overlay axes that rely on DefaultFromHostname. Use it from fastconfctl
// plan / PR-bots running on CI runners so the produced diff reflects
// the target environment instead of "ci-runner-7".
func (b *PlanBuilder[T]) WithHostname(host string) *PlanBuilder[T] {
	b.hostnameOverride = host
	return b
}

// Run executes the configured dry-run preview without mutating Manager
// state.
func (b *PlanBuilder[T]) Run(ctx context.Context) (*PlanResult[T], error) {
	if b == nil || b.m == nil {
		return nil, fmt.Errorf("fastconf: nil manager")
	}
	m := b.m
	staged, appendSlices, err := m.assemble(ctx, b.hostnameOverride)
	if err != nil {
		return nil, err
	}
	pc := &pipelineCtx[T]{
		reason:       "plan",
		staged:       staged,
		appendSlices: appendSlices,
		dryRun:       true,
	}
	if err := m.runStages(ctx, pc); err != nil {
		return nil, err
	}

	hash, err := canonicalHash(pc.target)
	if err != nil {
		return nil, fmt.Errorf("fastconf: hash: %w", err)
	}
	proposed := istate.NewSnapshot(
		pc.target,
		hash,
		time.Now().UnixNano(),
		pc.sources,
		m.gen.Load(), // not incremented; this is dry-run
		pc.origins,
		istate.ReloadCause{Reason: "plan", At: time.Now().UnixNano(), Tenant: m.tenant},
		nil,
		m.opts.SecretRedactor,
	)

	var diff []istate.DiffEntry
	if cur := m.state.Load(); cur != nil {
		diff = cur.Diff(proposed)
	}
	return &PlanResult[T]{
		Proposed:   proposed,
		Diff:       diff,
		Validators: pc.reports,
		Policies:   pc.policyViolations,
	}, nil
}
