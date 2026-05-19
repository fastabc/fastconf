package manager

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/secret"
	istate "github.com/fastabc/fastconf/internal/state"
)

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
			firstErr = fmt.Errorf("%w: secret %s@%s: %v", fcerr.ErrTransform, ref.Scheme, path, err)
			return v, false
		}
		if pc.origins != nil {
			pc.origins.Record(path, istate.SourceRef{
				Kind:     istate.LayerSecret,
				Path:     "secret://" + ref.Scheme,
				Priority: 9500,
			})
		}
		return plain, true
	})
	return firstErr
}
