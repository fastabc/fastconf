package fastconf

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/generator"
)

func TestPresetTesting(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	fsys := fstest.MapFS{
		"conf.d/base/app.yaml": &fstest.MapFile{Data: []byte("name: from-fs\n")},
	}
	mgr, err := New[cfg](context.Background(),
		PresetTesting(TestingOpts{FS: fsys}),
	)
	if err != nil {
		t.Fatalf("preset testing: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Name; got != "from-fs" {
		t.Fatalf("name=%q want from-fs", got)
	}
}

func TestPresetK8sAppliesDefaults(t *testing.T) {
	var o options
	PresetK8s(K8sOpts{})(&o)
	if o.Dir != "/etc/config" {
		t.Errorf("dir=%q want /etc/config", o.Dir)
	}
	if o.ProfileEnv != "APP_PROFILE" {
		t.Errorf("profileEnv=%q want APP_PROFILE", o.ProfileEnv)
	}
	if o.DefaultProf != "default" {
		t.Errorf("defaultProfile=%q want default", o.DefaultProf)
	}
	if !o.Strict {
		t.Errorf("strict=false; PresetK8s must default to strict=true")
	}
}

func TestPresetK8sOverridesAfterPreset(t *testing.T) {
	var o options
	PresetK8s(K8sOpts{Dir: "/p", ProfileEnv: "PROF"})(&o)
	WithStrict(false)(&o)
	if o.Strict {
		t.Error("explicit WithStrict(false) after preset must win")
	}
	if o.Dir != "/p" || o.ProfileEnv != "PROF" {
		t.Errorf("preset args not honoured: dir=%q env=%q", o.Dir, o.ProfileEnv)
	}
}

func TestPresetSidecarHistory(t *testing.T) {
	var o options
	PresetSidecar(SidecarOpts{})(&o)
	if o.HistoryCap != 16 {
		t.Errorf("historyCap=%d want 16", o.HistoryCap)
	}
	if o.Dir != "/etc/fastconfd" {
		t.Errorf("dir=%q want /etc/fastconfd", o.Dir)
	}
}

type cfg124 struct {
	App struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"app"`
}

// TestGenerator_RawLayerPriority verifies that two RawLayers emitted by
// the same Generator at distinct Priority values are stamped onto
// SourceRef with Kind=LayerGenerator and Priority offset into
// contracts.BandGenerator. This is the SPEC-A7 contract — the assemble
// stage walks layers in priority-ascending order, so higher
// RawLayer.Priority wins on conflicting keys.
func TestGenerator_RawLayerPriority(t *testing.T) {
	gen := &priorityGen{}
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("k: file\n")},
	}
	mgr, err := New[map[string]any](context.Background(),
		WithFS(fs),
		WithGenerator(gen),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	// Higher RawLayer.Priority wins between the two generator layers,
	// and the generator band (7000+) overrides the file base layer
	// (1000+) regardless.
	if got := (*mgr.Get())["k"]; got != "high" {
		t.Fatalf("expected high-priority generator layer to win, got %v", got)
	}
	var lowSeen, highSeen bool
	for _, s := range mgr.Snapshot().Sources() {
		if s.Kind != LayerGenerator {
			continue
		}
		switch s.Path {
		case "gen://prio/low":
			if s.Priority != 7000+10 {
				t.Errorf("low Priority offset: got %d want %d", s.Priority, 7010)
			}
			lowSeen = true
		case "gen://prio/high":
			if s.Priority != 7000+90 {
				t.Errorf("high Priority offset: got %d want %d", s.Priority, 7090)
			}
			highSeen = true
		}
	}
	if !lowSeen || !highSeen {
		t.Fatalf("expected both generator layers reported, low=%v high=%v", lowSeen, highSeen)
	}
}

type priorityGen struct{}

func (priorityGen) Name() string { return "prio" }
func (priorityGen) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{
		{Name: "low", Codec: "yaml", Data: []byte("k: low\n"), Priority: 10},
		{Name: "high", Codec: "yaml", Data: []byte("k: high\n"), Priority: 90},
	}, nil
}

func TestGenerator_BuildInfoOverlaysFiles(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
app:
  name: "fastconf"
  version: "0.0.0"
`)},
	}
	mgr, err := New[cfg124](context.Background(),
		WithFS(fs),
		WithGenerator(&generator.BuildInfo{
			Keys: map[string]string{
				"app.version": "1.2.3",
				"app.commit":  "abc",
			},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
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
