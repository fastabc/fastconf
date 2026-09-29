package obs

import (
	"context"
	"time"
)

// Tracer is a minimal dependency-free tracing surface.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, Span)
}

// Span is the minimal span surface Tracer implementations return.
type Span interface {
	End()
	RecordError(err error)
	SetAttribute(key string, value any)
}

type NoopTracer struct{}

func (NoopTracer) Start(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, NoopSpan{}
}

type NoopSpan struct{}

func (NoopSpan) End()                         {}
func (NoopSpan) RecordError(_ error)          {}
func (NoopSpan) SetAttribute(_ string, _ any) {}

// StartSpan handles a nil tracer gracefully and guarantees a non-nil Span.
func StartSpan(ctx context.Context, tracer Tracer, name string) (context.Context, Span) {
	if tracer == nil {
		return ctx, NoopSpan{}
	}
	c, sp := tracer.Start(ctx, name)
	if sp == nil {
		return c, NoopSpan{}
	}
	return c, sp
}

// CallbackContext bounds one user callback (audit sink, diff reporter):
// it adds a per-call deadline unless timeout is negative. The parent
// remains cancelable even when the per-call deadline is disabled.
func CallbackContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout < 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
