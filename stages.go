// stages.go hosts every built-in pipeline stage in defaultStages order.
package fastconf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/internal/typeinfo"
)

// --- merge ---

func runMerge[T any](_ context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	pipeline.merged = map[string]any{}
	pipeline.sources = make([]SourceRef, 0, len(pipeline.staged))
	pipeline.origins = provenance.NewIndex(m.opts.Provenance)
	mergeOpt := confmap.Options{Strict: m.opts.Strict, AppendSlices: pipeline.appendSlices, MergeKeys: pipeline.mergeKeys}
	for _, l := range pipeline.staged {
		if l.patch != nil {
			next, err := confmap.ApplyPatch(pipeline.merged, l.patch)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", fcerr.ErrMerge, l.src.Path, err)
			}
			pipeline.merged = next
			// Attribute only the paths the patch actually names — not the
			// whole merged tree. RecordTree here would tag every leaf as
			// patch-written, corrupting Explain for untouched
			// keys. Guard on origins to keep Off-level reloads walk-free.
			if pipeline.origins != nil {
				if paths, perr := confmap.PatchPaths(l.patch); perr == nil {
					for _, p := range paths {
						if p != "" {
							pipeline.origins.Record(p, l.src)
						}
					}
				}
			}
		} else {
			if err := confmap.Deep(pipeline.merged, l.data, mergeOpt); err != nil {
				return fmt.Errorf("%w: %s: %w", fcerr.ErrMerge, l.src.Path, err)
			}
			pipeline.origins.RecordTree("", l.data, l.src)
		}
		pipeline.sources = append(pipeline.sources, l.src)
	}
	return nil
}

// --- transform ---

func runTransform[T any](_ context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	for i, fn := range m.opts.Transforms {
		if err := fn(pipeline.merged); err != nil {
			return fmt.Errorf("%w: transform[%d]: %w", fcerr.ErrTransform, i, err)
		}
	}
	return nil
}

// --- secret ---

func runSecretResolve[T any](ctx context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	r := m.opts.SecretResolver
	if r == nil {
		return nil
	}
	var firstErr error
	secret.WalkLeaves(pipeline.merged, "", func(path string, v string) (string, bool) {
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
		if pipeline.origins != nil {
			pipeline.origins.Record(path, SourceRef{
				Kind:     LayerSecret,
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
func runTypedHooks[T any](_ context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	if m.typedHookPlan == nil {
		return nil
	}
	if err := m.typedHookPlan.Apply(pipeline.merged); err != nil {
		return fmt.Errorf("%w: typed-hook: %w", fcerr.ErrTransform, err)
	}
	return nil
}

// --- decode ---

func runDecode[T any](_ context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	pipeline.target = new(T)
	mode := m.opts.UnknownFields
	err := decodeInto(pipeline.merged, pipeline.target, m.opts.YAMLBridge, mode != UnknownIgnore)
	if mode == UnknownWarn && isUnknownField(err) {
		m.warnUnknownField(err)
		pipeline.target = new(T)
		err = decodeInto(pipeline.merged, pipeline.target, m.opts.YAMLBridge, false)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
	}
	// Hook results without a wire form (e.g. *url.URL) land after decoding.
	m.typedHookPlan.Assign(pipeline.merged, pipeline.target)
	// Struct-tag defaults (fc:"default=…") first, then the Defaulter
	// interface for computed defaults; both run before validation.
	if err := typeinfo.ApplyStructDefaults(pipeline.target); err != nil {
		return fmt.Errorf("%w: %w", fcerr.ErrDecode, err)
	}
	if d, ok := any(pipeline.target).(Defaulter); ok {
		d.Defaults()
	}
	return nil
}

// --- field-meta ---

func runFieldMetaCheck[T any](_ context.Context, _ *Manager[T], pipeline *pipelineState[T]) error {
	if pipeline.target == nil {
		return nil
	}
	violations := typeinfo.CheckFieldMeta(pipeline.target)
	if len(violations) == 0 {
		return nil
	}
	errs := make([]error, 0, len(violations))
	for _, v := range violations {
		if err := pipeline.finding("fastconf:field-meta", fmt.Errorf("%w: %s", fcerr.ErrInvalid, v.Msg)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// --- validate ---

func runValidate[T any](_ context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	for i, validate := range m.opts.Validators {
		name := ""
		if pipeline.dryRun {
			name = fmt.Sprintf("validator[%d]", i)
		}
		if err := pipeline.finding(name, validate(pipeline.target)); err != nil {
			return fmt.Errorf("%w: %w", fcerr.ErrInvalid, err)
		}
	}
	return nil
}

// --- policy ---

func runPolicy[T any](ctx context.Context, m *Manager[T], pipeline *pipelineState[T]) error {
	if len(m.opts.Policies) == 0 {
		return nil
	}
	polErr, warns := m.evaluatePolicies(ctx, pipeline.target, pipeline.reason)
	if pipeline.dryRun {
		// Plan mode: do not block the swap (there is no swap), but capture all
		// findings so the caller can surface them in PlanResult.Policies.
		pipeline.policyViolations = append(pipeline.policyViolations, warns...)
		if polErr != nil {
			pipeline.policyViolations = append(pipeline.policyViolations, polErr.Violations...)
		}
		return nil
	}
	for _, w := range warns {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf policy warning", slog.String("rule", w.Rule), slog.String("path", w.Path), slog.String("msg", w.Message))
	}
	if polErr != nil {
		return polErr
	}
	return nil
}
