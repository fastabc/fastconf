package zerolog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/fastabc/fastconf/integrations/log/internal/logtest"
	zerologadapter "github.com/fastabc/fastconf/integrations/log/zerolog"
	zlog "github.com/rs/zerolog"
)

func newLogger(buf *bytes.Buffer) (slog.Handler, *zlog.Logger) {
	zl := zlog.New(buf).Level(zlog.TraceLevel)
	return zerologadapter.NewHandler(zl, zerologadapter.Options{}), &zl
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
	// zerolog encodes Dur in ms by default
	if v, _ := got["dur"].(float64); v == 0 {
		t.Fatalf("dur missing: %v", got)
	}
	if _, ok := got["at"]; !ok {
		t.Fatalf("at missing: %v", got)
	}
}

func TestHandler_LevelGate(t *testing.T) {
	var buf bytes.Buffer
	zl := zlog.New(&buf).Level(zlog.TraceLevel)
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	h := zerologadapter.NewHandler(zl, zerologadapter.Options{Level: lv})
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
