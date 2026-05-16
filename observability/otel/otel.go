// Package otel adapts go.opentelemetry.io/otel to fastconf.Tracer.
//
// It is shipped as a separate Go module so that the core fastconf
// package never pulls in the OpenTelemetry SDK transitively.
package otel

import (
	"context"

	"github.com/fastabc/fastconf"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// New returns a fastconf.Tracer that delegates to the supplied OTel
// trace.Tracer. Pass otel.Tracer("github.com/fastabc/fastconf") for
// the default tracer provider, or any user-scoped tracer for tests.
func New(t oteltrace.Tracer) fastconf.Tracer {
	if t == nil {
		return nil
	}
	return otelTracer{t: t}
}

type otelTracer struct{ t oteltrace.Tracer }

func (o otelTracer) Start(ctx context.Context, name string) (context.Context, fastconf.Span) {
	c, sp := o.t.Start(ctx, name)
	return c, otelSpan{sp: sp}
}

type otelSpan struct{ sp oteltrace.Span }

func (s otelSpan) End() { s.sp.End() }

func (s otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.sp.RecordError(err)
	s.sp.SetStatus(codes.Error, err.Error())
}

func (s otelSpan) SetAttribute(key string, value any) {
	switch v := value.(type) {
	case string:
		s.sp.SetAttributes(attribute.String(key, v))
	case int:
		s.sp.SetAttributes(attribute.Int(key, v))
	case int64:
		s.sp.SetAttributes(attribute.Int64(key, v))
	case bool:
		s.sp.SetAttributes(attribute.Bool(key, v))
	case float64:
		s.sp.SetAttributes(attribute.Float64(key, v))
	}
}
