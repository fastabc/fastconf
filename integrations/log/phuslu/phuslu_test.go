package phuslu_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/fastabc/fastconf/integrations/log/internal/logtest"
	phusluadapter "github.com/fastabc/fastconf/integrations/log/phuslu"
	plog "github.com/phuslu/log"
)

func newLogger(buf *bytes.Buffer) (slog.Handler, *plog.Logger) {
	l := &plog.Logger{
		Level:  plog.TraceLevel,
		Writer: plog.IOWriter{Writer: buf},
	}
	return phusluadapter.NewHandler(l, phusluadapter.Options{}), l
}

func parse(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("json parse: %v; raw=%s", err, buf.String())
	}
	return out
}

func TestHandler_DurationAndTime(t *testing.T) {
	var buf bytes.Buffer
	h, _ := newLogger(&buf)
	l := slog.New(h)
	l.Info("timing",
		"dur", 1500*time.Millisecond,
		"at", time.Unix(1700000000, 0).UTC(),
	)
	got := parse(t, &buf)
	if _, ok := got["dur"]; !ok {
		t.Fatalf("dur missing: %v", got)
	}
	if _, ok := got["at"]; !ok {
		t.Fatalf("at missing: %v", got)
	}
}

func TestHandler_NilLoggerIsDisabled(t *testing.T) {
	h := phusluadapter.NewHandler(nil, phusluadapter.Options{})
	if h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("nil logger enabled error logging")
	}
	slog.New(h).Info("ignored")
}

func TestHandler_LevelGate(t *testing.T) {
	var buf bytes.Buffer
	pl := &plog.Logger{
		Level:  plog.TraceLevel,
		Writer: plog.IOWriter{Writer: &buf},
	}
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	h := phusluadapter.NewHandler(pl, phusluadapter.Options{Level: lv})
	l := slog.New(h)
	l.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("Info should be gated by Level=Warn: got %s", buf.String())
	}
	l.Warn("visible")
	if buf.Len() == 0 {
		t.Fatalf("Warn should pass Level gate")
	}
	// Hot-reload the gate
	buf.Reset()
	lv.Set(slog.LevelDebug)
	l.Debug("now visible")
	if buf.Len() == 0 {
		t.Fatalf("Debug should pass Level gate after lowering")
	}
}

func TestHandler_Contract(t *testing.T) {
	logtest.HandlerCases(t, func(buf *bytes.Buffer) slog.Handler { h, _ := newLogger(buf); return h })
}
