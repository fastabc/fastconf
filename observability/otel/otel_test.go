package otel_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	fcotel "github.com/fastabc/fastconf/observability/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOTelAdapter_RecordsStages(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
		}),
		fastconf.WithTracer(fcotel.New(tp.Tracer("fastconf-test"))),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	got := map[string]bool{}
	for _, sp := range exp.GetSpans() {
		got[sp.Name] = true
	}
	for _, want := range []string{"fastconf.reload", "fastconf.assemble", "fastconf.commit", "fastconf.merge", "fastconf.decode"} {
		if !got[want] {
			t.Errorf("missing span %q; got %v", want, got)
		}
	}
}

func TestTracer_SpanAttributesAndError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := fcotel.New(provider.Tracer("adapter"))
	ctx, parent := tracer.Start(context.Background(), "parent")
	_, child := tracer.Start(ctx, "child")
	for key, value := range map[string]any{"string": "value", "int": 1, "int64": int64(2), "bool": true, "float64": 1.5, "unsupported": []string{"x"}} {
		child.SetAttribute(key, value)
	}
	child.RecordError(nil)
	sentinel := errors.New("decode failed")
	child.RecordError(sentinel)
	child.End()
	parent.End()
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans=%v", spans)
	}
	got := spans[0]
	if got.Name != "child" || got.Parent.SpanID() != spans[1].SpanContext.SpanID() {
		t.Fatalf("parent propagation lost: %+v", got)
	}
	attrs := map[string]any{}
	for _, attr := range got.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	want := map[string]any{"string": "value", "int": int64(1), "int64": int64(2), "bool": true, "float64": 1.5}
	if !reflect.DeepEqual(attrs, want) {
		t.Fatalf("attrs=%v; want %v", attrs, want)
	}
	if got.Status.Code != codes.Error || got.Status.Description != sentinel.Error() || len(got.Events) != 1 {
		t.Fatalf("error span=%+v", got)
	}
}

func TestNew_NilTracer(t *testing.T) {
	if got := fcotel.New(nil); got != nil {
		t.Fatalf("New(nil)=%v; want nil", got)
	}
}
