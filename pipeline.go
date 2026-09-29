package fastconf

// Stages execute serially on the reload writer. Failure aborts publication;
// Plan shares the stages but collects findings without mutating manager state.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/policy"
)

// pipelineState holds assembly inputs and incremental stage outputs.
// Only commit may publish them; Plan leaves manager state and history unchanged.
type pipelineState[T any] struct {
	reason       string
	staged       []stagedLayer
	sources      []SourceRef
	merged       map[string]any
	origins      *provenance.Index
	target       *T
	appendSlices bool
	mergeKeys    map[string]string

	// dryRun = true skips the terminal swap/audit/history fan-out and
	// instructs validate to collect every report instead of bailing on
	// the first failure (Plan semantics).
	dryRun bool

	// reports is populated when dryRun is true.
	reports []ValidatorReport

	// policyViolations collects policy findings in dryRun mode.
	// In normal reload, violations that reach SeverityError abort the pipeline
	// via a *PolicyError; here they are captured and returned in PlanResult.
	policyViolations []policy.Violation
}

// stage is one step in the reload pipeline.
type stage[T any] struct {
	name string
	run  func(context.Context, *Manager[T], *pipelineState[T]) error
}

// defaultStages returns the canonical reload pipeline. Order matters:
// transform before decode (decode locks the type), defaults after
// decode (only zero fields), validate after defaults, policy last.
//
// runSecretResolve runs between transform and decode so plaintext is
// available to the decoder but not exposed to transformers that might
// log the merged tree.
func defaultStages[T any]() []stage[T] {
	return []stage[T]{
		{"merge", runMerge[T]},
		{"transform", runTransform[T]},
		{"secret", runSecretResolve[T]},
		{"typed-hooks", runTypedHooks[T]},
		{"decode", runDecode[T]},
		{"field-meta", runFieldMetaCheck[T]},
		{"validate", runValidate[T]},
		{"policy", runPolicy[T]},
	}
}

// runStages executes every stage in order, recording metrics and
// spans per stage. Returns the first error encountered; subsequent
// stages are skipped.
func (m *Manager[T]) runStages(ctx context.Context, pipeline *pipelineState[T]) error {
	for i, s := range m.stages {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := s.name
		elapsed, err := m.timed(ctx, "fastconf."+name, func(ctx context.Context, sp Span) error {
			err := s.run(ctx, m, pipeline)
			sp.SetAttribute("fastconf.stage", name)
			sp.SetAttribute("fastconf.stage.index", int64(i))
			sp.SetAttribute("fastconf.reload.reason", pipeline.reason)
			return err
		})
		if err != nil {
			m.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "stage error", slog.String("stage", name), slog.Duration("elapsed", elapsed), slog.Any("err", err))
			return err
		}
		m.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "stage done", slog.String("stage", name), slog.Duration("elapsed", elapsed))
	}
	return ctx.Err()
}

// runPipeline executes the shared commit/preview stages and hashes their result.
func (m *Manager[T]) runPipeline(ctx context.Context, a assemblyResult, reason string, dryRun bool) (*pipelineState[T], [32]byte, error) {
	p := &pipelineState[T]{reason: reason, staged: a.staged, appendSlices: a.appendSlices, mergeKeys: a.mergeKeys, dryRun: dryRun}
	if err := m.runStages(ctx, p); err != nil {
		return nil, [32]byte{}, err
	}
	hash, err := canonicalHash(p.target)
	if err != nil {
		return nil, hash, fmt.Errorf("fastconf: hash: %w", err)
	}
	return p, hash, nil
}

// finding retains successful validator reports in previews as well as failures.
func (p *pipelineState[T]) finding(name string, err error) error {
	if p.dryRun {
		p.reports = append(p.reports, ValidatorReport{Name: name, Err: err})
		return nil
	}
	return err
}
