package flog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastabc/fastconf/pkg/flog"
)

func newJSON(buf *bytes.Buffer, level slog.Level) *flog.Logger {
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level: level,
		// Strip "time" to make assertions stable.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return flog.New(slog.New(h))
}

// decodeOne parses a single JSON log line from buf and returns its
// top-level map. Resets buf so the next test phase starts clean.
func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	buf.Reset()
	if line == "" {
		t.Fatal("expected one log line, got empty buffer")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	return m
}

func TestEvent_AllFieldKindsRendered(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelDebug)

	log.Info().
		Str("reason", "boot").
		Int("layers", 3).
		Int64("rows", 1234567890).
		Uint64("generation", 42).
		Float64("ratio", 0.5).
		Bool("ok", true).
		Dur("elapsed", 7*time.Millisecond).
		Strs("paths", []string{"a", "b"}).
		Msg("reload swap")

	m := decodeOne(t, &buf)
	if m["msg"] != "reload swap" {
		t.Errorf("msg: %v", m["msg"])
	}
	if m["level"] != "INFO" {
		t.Errorf("level: %v", m["level"])
	}
	for k, want := range map[string]any{
		"reason":     "boot",
		"layers":     float64(3),
		"rows":       float64(1234567890),
		"generation": float64(42),
		"ratio":      0.5,
		"ok":         true,
	} {
		if got := m[k]; got != want {
			t.Errorf("%s: got %v (%T), want %v", k, got, got, want)
		}
	}
	if _, ok := m["elapsed"]; !ok {
		t.Errorf("missing elapsed")
	}
	if paths, ok := m["paths"].([]any); !ok || len(paths) != 2 {
		t.Errorf("paths: got %v", m["paths"])
	}
}

func TestEvent_LevelShortCircuit(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelWarn)

	// Debug is disabled — Info()/Debug() return nil; the chain must be inert.
	log.Debug().Str("k", "v").Int("n", 1).Err(errors.New("x")).Msg("ignored")
	log.Info().Str("k", "v").Msg("also ignored")

	if buf.Len() != 0 {
		t.Fatalf("expected empty output, got: %s", buf.String())
	}

	log.Warn().Str("k", "v").Msg("emitted")
	if !strings.Contains(buf.String(), `"msg":"emitted"`) {
		t.Fatalf("Warn did not emit: %s", buf.String())
	}
}

func TestEvent_ErrNilSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelDebug)

	log.Info().Str("k", "v").Err(nil).Msg("clean")
	m := decodeOne(t, &buf)
	if _, ok := m["err"]; ok {
		t.Fatalf("err key should be omitted when nil: %v", m)
	}

	log.Info().Err(errors.New("boom")).Msg("with err")
	m = decodeOne(t, &buf)
	if m["err"] != "boom" {
		t.Errorf("err: %v", m["err"])
	}
}

func TestContext_WithAttachesAttrsToEveryRecord(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelDebug)

	sub := log.With().Str("component", "pipeline").Int("worker", 7).Logger()

	sub.Info().Str("stage", "decode").Msg("done")
	m := decodeOne(t, &buf)
	if m["component"] != "pipeline" || m["worker"] != float64(7) || m["stage"] != "decode" {
		t.Errorf("merged attrs missing: %v", m)
	}

	// Parent logger must still emit without the derived attrs.
	log.Info().Str("stage", "merge").Msg("done")
	m = decodeOne(t, &buf)
	if _, ok := m["component"]; ok {
		t.Errorf("parent leaked derived attr: %v", m)
	}
}

func TestContext_GroupNestsSubsequentAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelDebug)

	sub := log.With().Group("stage").Str("name", "decode").Logger()
	sub.Info().Int("elapsed_ms", 12).Msg("ok")

	m := decodeOne(t, &buf)
	grp, ok := m["stage"].(map[string]any)
	if !ok {
		t.Fatalf("expected stage group: %v", m)
	}
	if grp["name"] != "decode" || grp["elapsed_ms"] != float64(12) {
		t.Errorf("group contents: %v", grp)
	}
}

func TestEvent_CtxVariantPropagatesContext(t *testing.T) {
	var buf bytes.Buffer

	type ctxKey struct{}
	captured := make(chan context.Context, 1)
	h := &capturingHandler{
		inner:    slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		captured: captured,
	}
	log := flog.New(slog.New(h))

	want := context.WithValue(context.Background(), ctxKey{}, "v")
	log.InfoCtx(want).Str("k", "v").Msg("hi")

	got := <-captured
	if got.Value(ctxKey{}) != "v" {
		t.Fatalf("ctx not propagated: %v", got)
	}
}

// capturingHandler forwards to an inner handler but records the ctx so
// tests can verify XxxCtx variants pass ctx through.
type capturingHandler struct {
	inner    slog.Handler
	captured chan<- context.Context
}

func (h *capturingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}
func (h *capturingHandler) Handle(ctx context.Context, r slog.Record) error {
	select {
	case h.captured <- ctx:
	default:
	}
	return h.inner.Handle(ctx, r)
}
func (h *capturingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &capturingHandler{inner: h.inner.WithAttrs(as), captured: h.captured}
}
func (h *capturingHandler) WithGroup(name string) slog.Handler {
	return &capturingHandler{inner: h.inner.WithGroup(name), captured: h.captured}
}

func TestEvent_PoolReusedConcurrently(t *testing.T) {
	var buf bytes.Buffer
	// Use a synchronizing handler so concurrent emits don't interleave writes.
	h := slog.NewJSONHandler(&syncWriter{w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := flog.New(slog.New(h))

	const N = 200
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func() {
			defer wg.Done()
			log.Info().Int("i", i).Str("kind", "concurrent").Msg("emit")
		}()
	}
	wg.Wait()

	if got := strings.Count(buf.String(), `"msg":"emit"`); got != N {
		t.Fatalf("emit count = %d, want %d", got, N)
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func TestEvent_SendEqualsEmptyMsg(t *testing.T) {
	var buf bytes.Buffer
	log := newJSON(&buf, slog.LevelDebug)

	log.Info().Str("k", "v").Send()
	m := decodeOne(t, &buf)
	if m["msg"] != "" {
		t.Errorf("Send msg: %v", m["msg"])
	}
	if m["k"] != "v" {
		t.Errorf("Send k: %v", m["k"])
	}
}

func TestLogger_SlogReturnsUnderlying(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	s := slog.New(h)
	log := flog.New(s)
	if log.Slog() != s {
		t.Fatal("Slog() must return the wrapped *slog.Logger")
	}
}

func TestNew_NilFallsBackToDefault(t *testing.T) {
	if flog.New(nil).Slog() == nil {
		t.Fatal("New(nil) must fall back to a non-nil slog.Logger")
	}
}
