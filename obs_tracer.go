package fastconf

// Tracing stays dependency-free in the core package; adapters such as the
// OpenTelemetry bridge live outside the root module surface.

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// Tracer is a minimal, dependency-free tracing surface that fastconf
// calls to mark the boundaries of each reload-pipeline stage. It is
// inspired by go.opentelemetry.io/otel but expressed without importing
// it so that the core module stays zero-dependency. Concrete adapters
// (OTel, Jaeger client, custom logger-as-trace) live in submodules.
//
// The framework guarantees:
//   - Start is always paired with Span.End (defer-ed).
//   - SetAttribute is called with primitive values: string, int64, bool.
//   - Errors flow into Span.RecordError; failed reloads also call End.
//   - All calls happen on the single reload goroutine; implementations
//     do NOT need to be safe for concurrent calls on the same Span.
//
// A nil Tracer (or Span returning nil) is always tolerated; the
// framework checks before dispatch.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, Span)
}

// Span is a type alias for contracts.Span (v0.10.0+). Existing callers
// that reference fastconf.Span continue to compile without any changes.
type Span = contracts.Span

// noopTracer is the default. It allocates nothing.
type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) End()                         {}
func (noopSpan) RecordError(_ error)          {}
func (noopSpan) SetAttribute(_ string, _ any) {}

// WithTracer installs a tracer. The framework opens spans for the
// reload root plus seven stages: assemble, merge, migration, transform,
// decode, validate, commit. Pass nil to keep the default no-op tracer.
func WithTracer(t Tracer) Option {
	return func(o *options) {
		if t != nil {
			o.tracer = t
		}
	}
}

// startSpan is a small helper that handles a nil tracer gracefully and
// guarantees a non-nil Span on return.
func (m *Manager[T]) startSpan(ctx context.Context, name string) (context.Context, Span) {
	if m.opts.tracer == nil {
		return ctx, noopSpan{}
	}
	c, sp := m.opts.tracer.Start(ctx, name)
	if sp == nil {
		return c, noopSpan{}
	}
	return c, sp
}
