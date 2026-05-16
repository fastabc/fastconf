package otel_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	fcotel "github.com/fastabc/fastconf/observability/otel"
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
	defer mgr.Close()

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
