package obs

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// Tracer is a minimal dependency-free tracing surface.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, Span)
}

type Span = contracts.Span

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
