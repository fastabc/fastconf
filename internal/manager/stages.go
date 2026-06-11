// stages.go hosts every built-in pipeline stage in defaultStages order.
// Each stage was previously a standalone file; they are colocated
// because the table in pipeline.go is the single scheduling authority
// and the stages average <35 lines each.
package manager

import (
	"context"
	"errors"
	"fmt"
	"maps"

	merger "github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/internal/fcerr"
	iopts "github.com/fastabc/fastconf/internal/options"
	ipipeline "github.com/fastabc/fastconf/internal/pipeline"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
	istate "github.com/fastabc/fastconf/internal/state"
)

// --- merge ---

func runMerge[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	pc.merged = map[string]any{}
	pc.sources = make([]istate.SourceRef, 0, len(pc.staged))
	pc.origins = provenance.NewIndex(m.opts.Provenance)
	mergeOpt := merger.Options{Strict: m.opts.Strict, AppendSlices: pc.appendSlices}
	// Combine _meta.yaml mergeKeys + programmatic mergeKeys.
	// Programmatic entries (WithMergeKeys) win on conflict.
	keys := map[string]string{}
	maps.Copy(keys, pc.mergeKeys)
	maps.Copy(keys, m.opts.MergeKeys)
	if len(keys) > 0 {
		mergeOpt.MergeKeys = keys
	}
	for _, l := range pc.staged {
		if l.patch != nil {
			next, err := merger.ApplyPatch(pc.merged, l.patch)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", fcerr.ErrPatch, l.src.Path, err)
			}
			pc.merged = next
			pc.origins.RecordTree("", pc.merged, l.src)
		} else {
			if err := merger.Deep(pc.merged, l.data, mergeOpt); err != nil {
				return fmt.Errorf("%w: %s: %w", fcerr.ErrMerge, l.src.Path, err)
			}
			pc.origins.RecordTree("", l.data, l.src)
		}
		pc.sources = append(pc.sources, l.src)
	}
	return nil
}

// --- migration ---

func runMigration[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.opts.MigrationRun == nil {
		return nil
	}
	if err := m.opts.MigrationRun.Migrate(pc.merged); err != nil {
		return fmt.Errorf("%w: migration: %w", fcerr.ErrTransform, err)
	}
	return nil
}

// --- transform ---

func runTransform[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	for _, tr := range m.opts.Transformers {
		if err := tr.Transform(pc.merged); err != nil {
			return fmt.Errorf("%w: %s: %w", fcerr.ErrTransform, tr.Name(), err)
		}
	}
	return nil
}

// --- secret ---

func runSecretResolve[T any](ctx context.Context, m *M[T], pc *pipelineCtx[T]) error {
	r := m.opts.SecretResolver
	if r == nil {
		return nil
	}
	var firstErr error
	secret.WalkLeaves(pc.merged, "", func(path string, v string) (string, bool) {
		if firstErr != nil {
			return v, false
		}
		ref, ok := r.Recognize(v)
		if !ok {
			return v, false
		}
		plain, err := r.Resolve(ctx, ref)
		if err != nil {
			firstErr = fmt.Errorf("%w: secret %s@%s: %w", fcerr.ErrTransform, ref.Scheme, path, err)
			return v, false
		}
		if pc.origins != nil {
			pc.origins.Record(path, istate.SourceRef{
				Kind:     istate.LayerSecret,
				Path:     "secret://" + ref.Scheme,
				Priority: prioritySecretResolved,
			})
		}
		return plain, true
	})
	return firstErr
}

// --- typed-hooks ---

// runTypedHooks rewrites leaves of the merged map to forms that the
// JSON decoder can natively assign to *T's fields ("30s" → int64
// nanoseconds for time.Duration, etc). The plan is precomputed once at
// Manager construction so this stage is a tree walk with no reflection.
func runTypedHooks[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.typedHookPlan == nil {
		return nil
	}
	if err := m.typedHookPlan.Apply(pc.merged); err != nil {
		return fmt.Errorf("%w: typed-hook: %w", fcerr.ErrTransform, err)
	}
	return nil
}

// --- decode ---

func runDecode[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	if m.opts.RawMapHook != nil {
		m.opts.RawMapHook(pc.merged)
	}
	pc.target = new(T)
	b, err := decodeInto(pc.merged, pc.target, m.opts.CodecBridge)
	if err != nil {
		return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
	}
	pc.mergedJSON = b
	if m.opts.StructDefaults != nil {
		if err := m.opts.StructDefaults(pc.target); err != nil {
			return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
		}
	}
	// iopts.Defaulter interface: auto-call Defaults() if *T implements it.
	if d, ok := any(pc.target).(iopts.Defaulter); ok {
		d.Defaults()
	}
	// WithDefaults: explicit callback for types that cannot implement
	// iopts.Defaulter.
	if m.opts.DefaulterFunc != nil {
		m.opts.DefaulterFunc(pc.target)
	}
	return nil
}

// --- field-meta ---

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
	// Collect all violations and join them so callers can inspect each one.
	errs := make([]error, len(violations))
	for i, v := range violations {
		errs[i] = fmt.Errorf("%w: %s", fcerr.ErrValidator, v.Msg)
	}
	return errors.Join(errs...)
}

// --- validate ---

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
			return fmt.Errorf("%w: %w", fcerr.ErrValidator, err)
		}
	}
	return nil
}

// --- policy ---

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
