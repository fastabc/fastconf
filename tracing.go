package fastconf

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/internal/fcerr"
	iobs "github.com/fastabc/fastconf/internal/obs"
)

// WithTracer installs a Tracer. Passing nil records a deferred error so
// a missing tracer fails loudly at New() rather than silently dropping
// every span.
func WithTracer(t Tracer) Option {
	return func(o *options) {
		if t == nil {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithTracer(nil)", fcerr.ErrFastConf))
			return
		}
		o.Tracer = t
	}
}

func (m *Manager[T]) startSpan(ctx context.Context, name string) (context.Context, iobs.Span) {
	return iobs.StartSpan(ctx, m.opts.Tracer, name)
}
