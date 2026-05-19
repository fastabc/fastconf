package manager

import (
	"context"

	"github.com/fastabc/fastconf/internal/obs"
)

func (m *M[T]) startSpan(ctx context.Context, name string) (context.Context, obs.Span) {
	return obs.StartSpan(ctx, m.opts.Tracer, name)
}
