package manager

// Pipeline core. The reload pipeline is expressed as a slice of named
// stages so that commit() and Plan() share exactly one definition of
// "what runs in what order". Adding a new stage means appending to
// defaultStages and dropping a stage_<name>.go file next to this one.
//
// Each stage is a pure function over *pipelineCtx[T]; it is invoked on
// the single reload goroutine, so no synchronisation is required
// between stages. A non-nil error aborts the pipeline; the atomic
// state pointer is never swapped on failure (failure-safe contract).

import (
	"context"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/obs"
	"github.com/fastabc/fastconf/internal/provenance"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/policy"
)

// pipelineCtx threads the in-flight reload state through the stages.
// Fields are populated incrementally:
//
//	assemble  → staged, sources, appendSlices
//	merge     → merged, origins
//	migration → mutates merged
//	transform → mutates merged
//	decode    → target, mergedJSON
//	defaults  → mutates target
//	validate  → (read-only on target)
//	policy    → (read-only on target)
//
// Manager-wide state (m.state, m.history, m.gen) is intentionally NOT
// part of pipelineCtx; only commit's terminal step touches those, and
// Plan() never does. This makes the per-reload data-flow explicit and
// removes the need for transient instance fields on Manager.
type pipelineCtx[T any] struct {
	reason       string
	staged       []stagedLayer
	sources      []istate.SourceRef
	merged       map[string]any
	mergedJSON   []byte // populated by stageDecode for canonicalHash reuse
	origins      *provenance.Index
	target       *T
	appendSlices bool

	// dryRun = true skips the terminal swap/audit/history fan-out and
	// instructs validate to collect every report instead of bailing on
	// the first failure (Plan semantics).
	dryRun bool

	// reports is populated when dryRun is true.
	reports []ValidatorReport

	// policyViolations collects policy findings in dryRun mode.
	// In normal reload, violations that reach SeverityError abort the pipeline
	// via a *fcerr.PolicyError; here they are captured and returned in PlanResult.
	policyViolations []policy.Violation
}

// stage is one step in the reload pipeline.
type stage[T any] struct {
	name string
	run  func(context.Context, *M[T], *pipelineCtx[T]) error
}

func (s stage[T]) Name() string { return s.name }
func (s stage[T]) Run(ctx context.Context, m *M[T], pc *pipelineCtx[T]) error {
	return s.run(ctx, m, pc)
}

// defaultStages returns the canonical reload pipeline. Order matters:
// migration before transform (transforms see the current schema),
// transform before decode (decode locks the type), defaults after
// decode (only zero fields), validate after defaults, policy last.
//
// stageSecretResolve runs between transform and decode so plaintext is
// available to the decoder but not exposed to transformers that might
// log the merged tree.
func defaultStages[T any]() []stage[T] {
	return []stage[T]{
		{"merge", runMerge[T]},
		{"migration", runMigration[T]},
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
func (m *M[T]) runStages(ctx context.Context, pc *pipelineCtx[T]) error {
	for i, s := range defaultStages[T]() {
		name := s.Name()
		start := time.Now()
		stCtx, sp := m.startSpan(ctx, "fastconf."+name)
		err := s.Run(stCtx, m, pc)
		elapsed := time.Since(start)
		if err != nil {
			sp.RecordError(err)
		}
		obs.EnrichAttrs(sp,
			contracts.Attr{Key: "fastconf.stage", Value: name},
			contracts.Attr{Key: "fastconf.stage.index", Value: int64(i)},
			contracts.Attr{Key: "fastconf.stage.elapsed_ms", Value: elapsed.Milliseconds()},
			contracts.Attr{Key: "fastconf.stage.success", Value: err == nil},
			contracts.Attr{Key: "fastconf.reload.reason", Value: pc.reason},
		)
		sp.End()
		m.opts.Metrics.StageDuration(name, elapsed, err == nil)
		if err != nil {
			m.opts.Log.DebugCtx(ctx).
				Str("stage", name).
				Dur("elapsed", elapsed).
				Err(err).
				Msg("stage error")
			return err
		}
		m.opts.Log.DebugCtx(ctx).
			Str("stage", name).
			Dur("elapsed", elapsed).
			Msg("stage done")
	}
	return nil
}
