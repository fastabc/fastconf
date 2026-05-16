package fastconf

import (
	"context"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/pkg/migration"
)

// Compile-time guard: defaultStages must yield ordered stage[T] values.
var _ []stage[map[string]any] = defaultStages[map[string]any]()

type recordingTracer struct {
	mu    sync.Mutex
	spans []*recordingSpan
}

func (r *recordingTracer) Start(ctx context.Context, name string) (context.Context, Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sp := &recordingSpan{name: name, attrs: map[string]any{}}
	r.spans = append(r.spans, sp)
	return ctx, sp
}

type recordingSpan struct {
	name  string
	ended bool
	err   error
	attrs map[string]any
}

func (s *recordingSpan) End()                         { s.ended = true }
func (s *recordingSpan) RecordError(err error)        { s.err = err }
func (s *recordingSpan) SetAttribute(k string, v any) { s.attrs[k] = v }

func TestTracer_EmitsAllStages(t *testing.T) {
	tr := &recordingTracer{}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nb: 2\n")},
	}
	mgr, err := New[map[string]any](context.Background(),
		WithFS(mfs),
		WithTracer(tr),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	tr.mu.Lock()
	got := map[string]bool{}
	for _, sp := range tr.spans {
		if !sp.ended {
			t.Errorf("span %q not ended", sp.name)
		}
		got[sp.name] = true
	}
	tr.mu.Unlock()

	want := []string{"fastconf.reload", "fastconf.assemble", "fastconf.commit", "fastconf.merge", "fastconf.decode"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected span %q missing; saw %v", w, got)
		}
	}
}

type stageRecorder struct {
	mu     sync.Mutex
	stages map[string]int
}

func newStageRecorder() *stageRecorder { return &stageRecorder{stages: map[string]int{}} }

func (s *stageRecorder) ReloadStarted()                         {}
func (s *stageRecorder) ReloadFinished(_ bool, _ time.Duration) {}
func (s *stageRecorder) StateGeneration(_ uint64)               {}
func (s *stageRecorder) LayersTotal(_ int)                      {}
func (s *stageRecorder) StageDuration(stage string, _ time.Duration, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.stages[stage]++
	} else {
		s.stages[stage+":err"]++
	}
}

func TestMetrics_PerStage(t *testing.T) {
	rec := newStageRecorder()
	mgr, err := New[map[string]any](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
		}),
		WithMetrics(rec),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, want := range []string{"assemble", "merge", "decode", "commit"} {
		if rec.stages[want] == 0 {
			t.Errorf("stage %q never recorded; got %v", want, rec.stages)
		}
	}
}

func TestPipeline_DefaultStagesExposeNames(t *testing.T) {
	stages := defaultStages[map[string]any]()
	// merge → migration → transform → secret (Phase 120) →
	// typed-hooks (Phase 130) → decode → field-meta (Phase 123) →
	// validate → policy
	want := []string{"merge", "migration", "transform", "secret", "typed-hooks", "decode", "field-meta", "validate", "policy"}
	if len(stages) != len(want) {
		t.Fatalf("len(stages) = %d, want %d", len(stages), len(want))
	}
	for i, s := range stages {
		if got := s.Name(); got != want[i] {
			t.Fatalf("stage[%d].Name() = %q, want %q", i, got, want[i])
		}
	}
}

type cfgMigration struct {
	DSN string `yaml:"dsn"`
}

func TestStage_MigrationRunsBeforeDecode(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("url: postgres://x\n")},
	}
	chain, err := migration.New(1,
		migration.Migration{From: 0, To: 1, Apply: func(m map[string]any) error {
			if v, ok := m["url"]; ok {
				m["dsn"] = v
				delete(m, "url")
			}
			return nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := New[cfgMigration](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithMigrations(func(m map[string]any) error {
			_, e := chain.Run(m)
			return e
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().DSN; got != "postgres://x" {
		t.Fatalf("dsn = %q want postgres://x", got)
	}
}

type mkCfg struct {
	Containers []struct {
		Name  string `json:"name"`
		Image string `json:"image"`
		Port  int    `json:"port"`
	} `json:"containers"`
}

func TestStage_MergeKeys(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
containers:
  - name: api
    image: img:v1
    port: 8080
  - name: sidecar
    image: side:v1
`)},
		"conf.d/overlays/prod/50.yaml": &fstest.MapFile{Data: []byte(`
containers:
  - name: api
    image: img:v2
`)},
	}
	mgr, err := New[mkCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
		WithProfile("prod"),
		WithMergeKeys(map[string]string{"containers": "name"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	cfg := mgr.Get()
	if len(cfg.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d: %+v", len(cfg.Containers), cfg.Containers)
	}
	var api, sidecar bool
	for _, c := range cfg.Containers {
		if c.Name == "api" {
			api = true
			if c.Image != "img:v2" {
				t.Errorf("api.image = %q, want img:v2", c.Image)
			}
			if c.Port != 8080 {
				t.Errorf("api.port should be preserved: got %d", c.Port)
			}
		}
		if c.Name == "sidecar" {
			sidecar = true
		}
	}
	if !api || !sidecar {
		t.Errorf("missing containers: api=%v sidecar=%v", api, sidecar)
	}
}
