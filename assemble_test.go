package fastconf

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/testutil"
)

func TestReload_AssemblyFailureRetainsWatchDirectories(t *testing.T) {
	failure := errors.New("source unavailable")
	provider := testutil.NewFakeProvider("failing", 0, nil)
	provider.LoadErr = failure
	for _, tc := range []struct {
		name string
		fs   fstest.MapFS
		opts []Option
		want error
	}{
		{"generator", fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("x: 1\n")}}, []Option{WithGenerator(assemblyErrorGenerator{failure})}, failure},
		{"provider", fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("x: 1\n")}}, []Option{WithProvider(provider)}, failure},
		{"no sources", fstest.MapFS{}, nil, ErrNoSources},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := append([]Option{WithFS(tc.fs)}, tc.opts...)
			m, err := newManager[map[string]any](context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer m.lifetimeCancel()
			if err := m.reload(context.Background(), "manual", ""); !errors.Is(err, tc.want) {
				t.Fatalf("reload error = %v; want %v", err, tc.want)
			}
			if !slices.Contains(m.scannedDirs, "conf.d/base") {
				t.Fatalf("failed reload discarded scanned directories: %v", m.scannedDirs)
			}
			if m.Snapshot() != nil {
				t.Fatal("failed reload published a snapshot")
			}
		})
	}
}

type assemblyErrorGenerator struct{ err error }

func (assemblyErrorGenerator) Name() string { return "failing" }
func (g assemblyErrorGenerator) Generate(context.Context) ([]contracts.RawLayer, error) {
	return nil, g.err
}

// countingProvider wraps a static map and counts Load calls.
type countingProvider struct {
	loads atomic.Int64
	data  map[string]any
}

// TestAssemble_CtxCanceled_NoProviderCalled verifies that assemble returns
// context.Canceled immediately without ever calling
// Provider.Load when the context is already done.
func TestAssemble_CtxCanceled_NoProviderCalled(t *testing.T) {
	cp := &countingProvider{data: map[string]any{"x": 1}}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("x: 1\n")},
	}
	m, err := New[map[string]any](context.Background(),
		WithFS(mfs),
		WithProvider(cp),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = m.Close() }()

	// Capture the load count after successful New (includes the initial reload).
	baseline := cp.loads.Load()

	// Cancel a fresh context BEFORE calling assemble.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = m.assemble(canceledCtx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("assemble(canceledCtx) err = %v, want context.Canceled", err)
	}

	// Provider.Load must NOT have been called between the cancel and the assert.
	if delta := cp.loads.Load() - baseline; delta != 0 {
		t.Fatalf("Provider.Load called %d time(s) after context canceled; want 0", delta)
	}
}

func (c *countingProvider) Name() string { return "counting" }

func (c *countingProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 0}
}

func (c *countingProvider) Load(_ context.Context) (contracts.Snapshot, error) {
	c.loads.Add(1)
	out := make(map[string]any, len(c.data))
	for k, v := range c.data {
		out[k] = v
	}
	return contracts.Snapshot{Map: out}, nil
}

type generatedConfig struct {
	App struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"app"`
}

func TestGenerator_RawLayerPriority(t *testing.T) {
	low := contracts.RawLayer{Name: "low", Codec: "yaml", Data: []byte("k: low\n"), Priority: 10}
	high := contracts.RawLayer{Name: "high", Codec: "yaml", Data: []byte("k: high\n"), Priority: 90}
	for _, tc := range []struct {
		name   string
		layers []contracts.RawLayer
	}{
		{"ascending", []contracts.RawLayer{low, high}},
		{"descending", []contracts.RawLayer{high, low}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := Load[map[string]any](context.Background(), WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("k: file\n")}}), WithGenerator(priorityGen{tc.layers}))
			if err != nil {
				t.Fatal(err)
			}
			if got := (*snap.Value())["k"]; got != "high" {
				t.Fatalf("merged k=%v; want high", got)
			}
			priorities := map[string]int{}
			for _, source := range snap.Sources() {
				if source.Kind == LayerGenerator {
					priorities[source.Path] = source.Priority
				}
			}
			if priorities["gen://prio/low"] != 7010 || priorities["gen://prio/high"] != 7090 {
				t.Fatalf("generator sources=%v", priorities)
			}
		})
	}
}

type priorityGen struct{ layers []contracts.RawLayer }

func (priorityGen) Name() string { return "prio" }

func (g priorityGen) Generate(context.Context) ([]contracts.RawLayer, error) { return g.layers, nil }

func TestGenerator_OverlaysFiles(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
app:
  name: "fastconf"
  version: "0.0.0"
`)},
	}
	mgr, err := New[generatedConfig](context.Background(),
		WithFS(fs),
		WithGenerator(buildInfoGen{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	got := mgr.Get()
	if got.App.Name != "fastconf" {
		t.Fatalf("name should come from file: got %q", got.App.Name)
	}
	if got.App.Version != "1.2.3" {
		t.Fatalf("generator should win on version: got %q", got.App.Version)
	}
	if got.App.Commit != "abc" {
		t.Fatalf("generator should set commit: got %q", got.App.Commit)
	}
}

type buildInfoGen struct{}

func (buildInfoGen) Name() string { return "buildinfo" }

func (buildInfoGen) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{{
		Name:  "info",
		Codec: "json",
		Data:  []byte(`{"app":{"version":"1.2.3","commit":"abc"}}`),
	}}, nil
}

func TestProfile_ProfilesMatchExpression(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod-eu/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & eu\n")},
		"conf.d/overlays/prod-eu/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-eu\n")},
		"conf.d/overlays/prod-us/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & us\n")},
		"conf.d/overlays/prod-us/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-us\n")},
		"conf.d/overlays/canary/_meta.yaml":  &fstest.MapFile{Data: []byte("match: canary\n")},
		"conf.d/overlays/canary/00.yaml":     &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod", "eu"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod-eu" {
		t.Fatalf("expected prod-eu overlay to win, got %q", got)
	}
}

func TestProfile_FallbackToNameMembership(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/dev/00.yaml":  &fstest.MapFile{Data: []byte("name: dev\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("name-membership fallback failed, got %q", got)
	}
}

func TestProfile_GlobalProfileExpr(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":               &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml":      &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/canary/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod\n")},
		"conf.d/overlays/canary/00.yaml":    &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod"}, Match: "!canary"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("global expr should suppress canary, got %q", got)
	}
}
