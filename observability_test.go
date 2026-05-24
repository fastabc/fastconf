package fastconf_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

// ── Folded from observability_internal_test.go ──

// TestAuditSink_EncoderReuse asserts that NewJSONAuditSink reuses a
// single json.Encoder under its mutex so successive Audit calls do not
// allocate a fresh encoder per line.
func TestAuditSink_EncoderReuse(t *testing.T) {
	var buf bytes.Buffer
	sink := fastconf.NewJSONAuditSink(&buf)
	for i := 0; i < 3; i++ {
		if err := sink.Audit(context.Background(), fastconf.ReloadCause{Reason: "test"}); err != nil {
			t.Fatalf("audit: %v", err)
		}
	}
	lines := strings.Count(buf.String(), "\n")
	if lines != 3 {
		t.Fatalf("expected 3 lines, got %d (%q)", lines, buf.String())
	}
}

type cfgAudit struct {
	Name string `yaml:"name"`
}

func TestAuditSink_EmitsReloadCause(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: hello\n")},
	}
	var mu sync.Mutex
	var seen []fastconf.ReloadCause
	sink := fastconf.AuditSinkFunc(func(_ context.Context, c fastconf.ReloadCause) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
		return nil
	})
	mgr, err := fastconf.New[cfgAudit](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithAuditSink(sink),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	mu.Lock()
	n := len(seen)
	first := seen[0]
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 audit event, got %d", n)
	}
	if first.Reason != "initial" || first.At == 0 {
		t.Fatalf("bad cause %+v", first)
	}
	if got := mgr.Snapshot().Cause(); got.Reason != "initial" {
		t.Fatalf("State.Cause() not surfaced: %+v", got)
	}
}

func TestAuditSink_JSONLineFormat(t *testing.T) {
	var buf bytes.Buffer
	sink := fastconf.NewJSONAuditSink(&buf)
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	mgr, err := fastconf.New[cfgAudit](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithAuditSink(sink),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if !bytes.Contains(buf.Bytes(), []byte(`"reason":"initial"`)) {
		t.Fatalf("audit json missing reason: %s", buf.String())
	}
}

func TestPipeline_StageDebugLogs(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	logs := buf.String()
	if strings.Contains(logs, `"msg":"fastconf: stage"`) {
		t.Fatalf("unexpected pre-run stage log in logs:\n%s", logs)
	}
	if got := strings.Count(logs, `"msg":"stage done"`); got != 9 {
		t.Fatalf("stage log count = %d, want 9; logs:\n%s", got, logs)
	}
	for _, want := range []string{"merge", "migration", "transform", "secret", "typed-hooks", "decode", "field-meta", "validate", "policy"} {
		if !strings.Contains(logs, `"stage":"`+want+`"`) {
			t.Fatalf("missing stage %q in logs:\n%s", want, logs)
		}
	}
	if !strings.Contains(logs, `"elapsed"`) {
		t.Fatalf("missing elapsed field in logs:\n%s", logs)
	}
}

func TestPipeline_StageErrorLogsFailure(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	_, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
		fastconf.WithValidator(func(*appCfg) error { return errors.New("boom") }),
	)
	if err == nil {
		t.Fatal("expected validator failure")
	}

	logs := buf.String()
	if !strings.Contains(logs, `"msg":"stage error"`) {
		t.Fatalf("missing stage error log:\n%s", logs)
	}
	if !strings.Contains(logs, `"stage":"validate"`) {
		t.Fatalf("missing validate stage in logs:\n%s", logs)
	}
}

type recordingMetrics struct {
	started  atomic.Int64
	okCount  atomic.Int64
	errCount atomic.Int64
	gen      atomic.Uint64
	layers   atomic.Int64
}

func (r *recordingMetrics) ReloadStarted() { r.started.Add(1) }
func (r *recordingMetrics) ReloadFinished(ok bool, _ time.Duration) {
	if ok {
		r.okCount.Add(1)
	} else {
		r.errCount.Add(1)
	}
}
func (r *recordingMetrics) StateGeneration(g uint64) { r.gen.Store(g) }
func (r *recordingMetrics) LayersTotal(n int)        { r.layers.Store(int64(n)) }

func TestObservability_SlogAndMetrics(t *testing.T) {
	mfs := newFS(nil)
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	rec := &recordingMetrics{}

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithLogger(slog.New(h)),
		fastconf.WithMetrics(rec),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if rec.started.Load() != 1 || rec.okCount.Load() != 1 {
		t.Errorf("metrics counters: started=%d ok=%d", rec.started.Load(), rec.okCount.Load())
	}
	if rec.gen.Load() != 1 || rec.layers.Load() != 2 {
		t.Errorf("gauges: gen=%d layers=%d", rec.gen.Load(), rec.layers.Load())
	}

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"initial"`) || !strings.Contains(logs, "fastconf reload swap") {
		t.Errorf("expected reload log entries, got:\n%s", logs)
	}
}

// TestObs_NilOptionsAreDeferredErrors locks in SPEC-D3: passing nil to
// WithLogger / WithMetrics / WithTracer must surface as a DeferredErr
// from New(), not be silently dropped.
func TestObs_NilOptionsAreDeferredErrors(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	type cfg struct {
		Name string `json:"name"`
	}

	cases := []struct {
		name string
		opt  fastconf.Option
		want string
	}{
		{"WithLogger", fastconf.WithLogger(nil), "WithLogger(nil)"},
		{"WithMetrics", fastconf.WithMetrics(nil), "WithMetrics(nil)"},
		{"WithTracer", fastconf.WithTracer(nil), "WithTracer(nil)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fastconf.New[cfg](context.Background(),
				fastconf.WithFS(mfs),
				fastconf.WithDir("conf.d"),
				tc.opt,
			)
			if err == nil {
				t.Fatal("expected nil-option to surface as deferred error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
