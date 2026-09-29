package fastconf

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/internal/testutil"
	"github.com/fastabc/fastconf/transform"
)

func TestTracer_EmitsAllStages(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nb: 2\n")},
	}
	mgr, err := New[map[string]any](context.Background(),
		func(o *options) { o.FS = mfs },
		func(o *options) { o.Tracer = tr },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	got := map[string]int{}
	for _, sp := range tr.Spans() {
		if !sp.Ended {
			t.Errorf("span %q not ended", sp.Name)
		}
		got[sp.Name]++
	}

	want := map[string]int{
		"fastconf.reload": 1, "fastconf.assemble": 1, "fastconf.commit": 1,
		"fastconf.merge": 1, "fastconf.transform": 1, "fastconf.secret": 1,
		"fastconf.typed-hooks": 1, "fastconf.decode": 1, "fastconf.field-meta": 1,
		"fastconf.validate": 1, "fastconf.policy": 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spans = %v, want %v", got, want)
	}
}

// observerFunc adapts a function to Observer for in-package tests (the
// observe package imports fastconf, so it cannot be used here).
type observerFunc func(context.Context, Event)

func (f observerFunc) Observe(ctx context.Context, e Event) { f(ctx, e) }

func TestObserver_PerStage(t *testing.T) {
	var mu sync.Mutex
	stages := map[string]int{}
	rec := observerFunc(func(_ context.Context, e Event) {
		if s, ok := e.(StageFinished); ok {
			mu.Lock()
			defer mu.Unlock()
			if s.Err == nil {
				stages[s.Stage]++
			} else {
				stages[s.Stage+":err"]++
			}
		}
	})
	mgr, err := New[map[string]any](context.Background(),
		func(o *options) {
			o.FS = fstest.MapFS{
				"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
			}
		},
		WithObserver(rec),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{
		"assemble": 1, "merge": 1, "transform": 1, "secret": 1, "typed-hooks": 1,
		"decode": 1, "field-meta": 1, "validate": 1, "policy": 1, "commit": 1,
	}
	if !reflect.DeepEqual(stages, want) {
		t.Errorf("stages = %v, want %v", stages, want)
	}
}

func TestPipeline_StageOrder(t *testing.T) {
	stages := defaultStages[map[string]any]()
	want := []string{"merge", "transform", "secret", "typed-hooks", "decode", "field-meta", "validate", "policy"}
	if len(stages) != len(want) {
		t.Fatalf("len(stages) = %d, want %d", len(stages), len(want))
	}
	for i, s := range stages {
		if got := s.name; got != want[i] {
			t.Fatalf("stage[%d].name = %q, want %q", i, got, want[i])
		}
	}
}

type cfgMigration struct {
	DSN string `yaml:"dsn"`
}

func TestStage_MigrationTransformRunsBeforeDecode(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("url: postgres://x\n")},
	}
	chain, err := transform.New(1,
		transform.Migration{From: 0, To: 1, Apply: func(m map[string]any) error {
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
		func(o *options) {
			o.FS = mfs
			o.Dir = "conf.d"
		},
		WithTransform(func(m map[string]any) error {
			_, e := chain.Run(m)
			return e
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
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
		func(o *options) {
			o.FS = fs
			o.Dir = "conf.d"
			o.ProfileNames = []string{"prod"}
		},
		WithMergeKeys(map[string]string{"containers": "name"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
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

func TestStage_MetaMergeKeysApplyToReloadAndPlan(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/_meta.yaml": &fstest.MapFile{Data: []byte(`
spec:
  mergeKeys:
    containers: name
`)},
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
		func(o *options) {
			o.FS = fs
			o.Dir = "conf.d"
			o.ProfileNames = []string{"prod"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	cfg := mgr.Get()
	if len(cfg.Containers) != 2 {
		t.Fatalf("reload should preserve sidecar via meta mergeKeys, got %+v", cfg.Containers)
	}
	if cfg.Containers[0].Image != "img:v2" || cfg.Containers[0].Port != 8080 {
		t.Fatalf("reload merged api = %+v, want image v2 with preserved port", cfg.Containers[0])
	}

	fs["conf.d/overlays/prod/50.yaml"] = &fstest.MapFile{Data: []byte(`
containers:
  - name: api
    image: img:v3
`)}
	plan, err := mgr.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proposed := plan.Proposed.Value()
	if len(proposed.Containers) != 2 {
		t.Fatalf("plan should preserve sidecar via meta mergeKeys, got %+v", proposed.Containers)
	}
	if proposed.Containers[0].Image != "img:v3" || proposed.Containers[0].Port != 8080 {
		t.Fatalf("plan merged api = %+v, want image v3 with preserved port", proposed.Containers[0])
	}
	if got := mgr.Get().Containers[0].Image; got != "img:v2" {
		t.Fatalf("Plan mutated live state: image = %q, want img:v2", got)
	}
}

// TestObserverEventsCostNothingWithoutObservers pins that a manager with no
// observers allocates no events: emit is guarded so event values are never
// boxed into the Event interface.
func TestObserverEventsCostNothingWithoutObservers(t *testing.T) {
	m := newBenchManager(t)
	defer func() { _ = m.Close() }()
	allocs := testing.AllocsPerRun(50, func() {
		m.emitStage("merge", time.Millisecond, nil)
		m.emitReload("r", time.Millisecond, nil)
	})
	if allocs != 0 {
		t.Fatalf("emit helpers allocated %.0f times with no observers", allocs)
	}
}

// TestMergeKeysCachedPerMeta verifies: the combined _meta.yaml +
// WithMergeKeys table is built once per _meta.yaml content, not per reload,
// and rebuilt when _meta.yaml changes.
func TestMergeKeysCachedPerMeta(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/_meta.yaml":   &fstest.MapFile{Data: []byte("spec:\n  mergeKeys:\n    containers: name\n")},
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\n")},
	}
	m, err := New[map[string]any](context.Background(), WithFS(fs), WithMergeKeys(map[string]string{"services": "id"}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	first, err := m.assemble(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.assemble(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if first.mergeKeys["containers"] != "name" || first.mergeKeys["services"] != "id" {
		t.Fatalf("combined mergeKeys = %v", first.mergeKeys)
	}
	if reflect.ValueOf(first.mergeKeys).UnsafePointer() != reflect.ValueOf(second.mergeKeys).UnsafePointer() {
		t.Fatal("mergeKeys rebuilt although _meta.yaml did not change")
	}
	fs["conf.d/_meta.yaml"] = &fstest.MapFile{Data: []byte("spec:\n  mergeKeys:\n    containers: id\n")}
	third, err := m.assemble(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if third.mergeKeys["containers"] != "id" {
		t.Fatalf("mergeKeys not rebuilt after _meta.yaml changed: %v", third.mergeKeys)
	}
}
