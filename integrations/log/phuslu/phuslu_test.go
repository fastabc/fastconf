package phuslu_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	plog "github.com/phuslu/log"

	phusluadapter "github.com/fastabc/fastconf/integrations/log/phuslu"
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

func TestHandler_BasicInfo(t *testing.T) {
	var buf bytes.Buffer
	h, _ := newLogger(&buf)
	l := slog.New(h)
	l.Info("reload swap", "reason", "watcher", "generation", int64(7))
	got := parse(t, &buf)
	if got["level"] != "info" {
		t.Fatalf("level got %v", got["level"])
	}
	if got["message"] != "reload swap" {
		t.Fatalf("message got %v", got["message"])
	}
	if got["reason"] != "watcher" {
		t.Fatalf("reason got %v", got["reason"])
	}
	if v, ok := got["generation"].(float64); !ok || int(v) != 7 {
		t.Fatalf("generation got %v (%T)", got["generation"], got["generation"])
	}
}

func TestHandler_LevelMapping(t *testing.T) {
	cases := []struct {
		slogLvl slog.Level
		want    string
	}{
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		h, _ := newLogger(&buf)
		l := slog.New(h)
		l.Log(context.Background(), c.slogLvl, "msg")
		got := parse(t, &buf)
		if got["level"] != c.want {
			t.Fatalf("for %v: level got %v want %s", c.slogLvl, got["level"], c.want)
		}
	}
}

func TestHandler_GroupPrefix(t *testing.T) {
	var buf bytes.Buffer
	h, _ := newLogger(&buf)
	l := slog.New(h).WithGroup("stage")
	l.Info("done", "name", "decode", "elapsed_ms", int64(2))
	got := parse(t, &buf)
	if got["stage.name"] != "decode" {
		t.Fatalf("group key got %v (full=%v)", got["stage.name"], got)
	}
	if v, _ := got["stage.elapsed_ms"].(float64); int(v) != 2 {
		t.Fatalf("elapsed got %v", got["stage.elapsed_ms"])
	}
}

func TestHandler_WithAttrsAccumulate(t *testing.T) {
	var buf bytes.Buffer
	h, _ := newLogger(&buf)
	l := slog.New(h).With("tenant", "acme")
	l.Info("ok")
	got := parse(t, &buf)
	if got["tenant"] != "acme" {
		t.Fatalf("attrs missing: %v", got)
	}
}

func TestHandler_ErrorAttr(t *testing.T) {
	var buf bytes.Buffer
	h, _ := newLogger(&buf)
	l := slog.New(h)
	l.Error("boom", "err", errors.New("disk full"))
	got := parse(t, &buf)
	if got["err"] != "disk full" {
		t.Fatalf("error attr got %v", got["err"])
	}
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

func TestHandler_NilLoggerIsNoop(t *testing.T) {
	h := phusluadapter.NewHandler(nil, phusluadapter.Options{})
	l := slog.New(h)
	l.Info("should not panic")
	// no assertion: nil logger ⇒ no output, no panic
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
