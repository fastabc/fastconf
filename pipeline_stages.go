package fastconf

// Pipeline stages. The reload pipeline is expressed as a slice of
// named stages so that commit() and Plan() share exactly one definition
// of "what runs in what order". Adding a new stage means appending to
// defaultStages, not editing two ~100-line functions in lock-step.
//
// Each stage is a pure function over *pipelineCtx[T]; it is invoked on
// the single reload goroutine, so no synchronisation is required
// between stages. A non-nil error aborts the pipeline; the atomic
// state pointer is never swapped on failure (failure-safe contract).

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/obs"
	"github.com/fastabc/fastconf/pkg/merger"
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
	sources      []SourceRef
	merged       map[string]any
	mergedJSON   []byte // populated by stageDecode for canonicalHash reuse
	origins      *OriginIndex
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
	// via a *PolicyError; here they are captured and returned in PlanResult.
	policyViolations []policy.Violation
}

// stage is one step in the reload pipeline.
type stage[T any] struct {
	name string
	run  func(context.Context, *Manager[T], *pipelineCtx[T]) error
}

func (s stage[T]) Name() string { return s.name }
func (s stage[T]) Run(ctx context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
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

// runTypedHooks rewrites leaves of the merged map to forms that the
// JSON decoder can natively assign to *T's fields ("30s" → int64
// nanoseconds for time.Duration, etc). The plan is precomputed once at
// Manager construction so this stage is a tree walk with no reflection.
func runTypedHooks[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	if m.typedHookPlan == nil {
		return nil
	}
	if err := m.typedHookPlan.Apply(pc.merged); err != nil {
		return fmt.Errorf("%w: typed-hook: %v", ErrTransform, err)
	}
	return nil
}

func runMerge[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	pc.merged = map[string]any{}
	pc.sources = make([]SourceRef, 0, len(pc.staged))
	pc.origins = newOriginIndex(m.opts.provenance)
	mergeOpt := merger.Options{Strict: m.opts.strict, AppendSlices: pc.appendSlices}
	// Combine _meta.yaml mergeKeys + programmatic mergeKeys.
	// Programmatic entries (WithMergeKeys) win on conflict.
	keys := map[string]string{}
	if mk := m.lastMergeKeys.Load(); mk != nil {
		maps.Copy(keys, *mk)
	}
	maps.Copy(keys, m.opts.mergeKeys)
	if len(keys) > 0 {
		mergeOpt.MergeKeys = keys
	}
	for _, l := range pc.staged {
		if l.patch != nil {
			next, err := merger.ApplyPatch(pc.merged, l.patch)
			if err != nil {
				return fmt.Errorf("%w: %s: %v", ErrPatch, l.src.Path, err)
			}
			pc.merged = next
			pc.origins.recordTree("", pc.merged, l.src)
		} else {
			if err := merger.Deep(pc.merged, l.data, mergeOpt); err != nil {
				return fmt.Errorf("%w: %s: %v", ErrMerge, l.src.Path, err)
			}
			pc.origins.recordTree("", l.data, l.src)
		}
		pc.sources = append(pc.sources, l.src)
	}
	return nil
}

func runMigration[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	if m.opts.migrationRun == nil {
		return nil
	}
	if err := m.opts.migrationRun.Migrate(pc.merged); err != nil {
		return fmt.Errorf("%w: migration: %v", ErrTransform, err)
	}
	return nil
}

func runTransform[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	for _, tr := range m.opts.transformers {
		if err := tr.Transform(pc.merged); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrTransform, tr.Name(), err)
		}
	}
	return nil
}

func runDecode[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	if m.opts.rawMapHook != nil {
		m.opts.rawMapHook(pc.merged)
	}
	pc.target = new(T)
	b, err := decodeInto(pc.merged, pc.target, m.opts.codecBridge)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecode, err)
	}
	pc.mergedJSON = b
	if m.opts.structDefaults != nil {
		if err := m.opts.structDefaults(pc.target); err != nil {
			return fmt.Errorf("%w: %v", ErrDecode, err)
		}
	}
	// Defaulter interface: auto-call Defaults() if *T implements it.
	if d, ok := any(pc.target).(Defaulter); ok {
		d.Defaults()
	}
	// WithDefaulterFunc: explicit callback for types that cannot implement Defaulter.
	if m.opts.defaulterFunc != nil {
		m.opts.defaulterFunc(pc.target)
	}
	return nil
}

func runValidate[T any](_ context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	if pc.dryRun {
		// Plan semantics: collect every result, never abort.
		pc.reports = make([]ValidatorReport, 0, len(m.opts.validators))
		for i, v := range m.opts.validators {
			pc.reports = append(pc.reports, ValidatorReport{
				Name: fmt.Sprintf("validator[%d]", i),
				Err:  v.fn(pc.target),
			})
		}
		return nil
	}
	for _, v := range m.opts.validators {
		if err := v.fn(pc.target); err != nil {
			return fmt.Errorf("%w: %v", ErrValidator, err)
		}
	}
	return nil
}

func runPolicy[T any](ctx context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	if len(m.opts.policies) == 0 {
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
		m.opts.log.Warn().
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

// runStages executes every stage in order, recording metrics and
// spans per stage. Returns the first error encountered; subsequent
// stages are skipped.
func (m *Manager[T]) runStages(ctx context.Context, pc *pipelineCtx[T]) error {
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
		m.opts.metrics.StageDuration(name, elapsed, err == nil)
		if err != nil {
			m.opts.log.DebugCtx(ctx).
				Str("stage", name).
				Dur("elapsed", elapsed).
				Err(err).
				Msg("stage error")
			return err
		}
		m.opts.log.DebugCtx(ctx).
			Str("stage", name).
			Dur("elapsed", elapsed).
			Msg("stage done")
	}
	return nil
}
