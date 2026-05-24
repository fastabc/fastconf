package manager

import (
	"context"
	"fmt"
	"maps"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/provenance"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/merger"
)

func runMerge[T any](_ context.Context, m *M[T], pc *pipelineCtx[T]) error {
	pc.merged = map[string]any{}
	pc.sources = make([]istate.SourceRef, 0, len(pc.staged))
	pc.origins = provenance.NewIndex(m.opts.Provenance)
	mergeOpt := merger.Options{Strict: m.opts.Strict, AppendSlices: pc.appendSlices}
	// Combine _meta.yaml mergeKeys + programmatic mergeKeys.
	// Programmatic entries (WithMergeKeys) win on conflict.
	keys := map[string]string{}
	if mk := m.lastMergeKeys.Load(); mk != nil {
		maps.Copy(keys, *mk)
	}
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
