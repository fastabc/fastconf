package fastconf

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/pkg/generator"
)

func TestPresetTesting(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	fsys := fstest.MapFS{
		"conf.d/base/app.yaml": &fstest.MapFile{Data: []byte("name: from-fs\n")},
	}
	mgr, err := New[cfg](t.Context(),
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
	o := defaultOptions()
	PresetK8s(K8sOpts{})(&o)
	if o.dir != "/etc/config" {
		t.Errorf("dir=%q want /etc/config", o.dir)
	}
	if o.profileEnv != "APP_PROFILE" {
		t.Errorf("profileEnv=%q want APP_PROFILE", o.profileEnv)
	}
	if o.defaultProf != "default" {
		t.Errorf("defaultProfile=%q want default", o.defaultProf)
	}
	if !o.strict {
		t.Errorf("strict=false; PresetK8s must default to strict=true")
	}
}

func TestPresetK8sOverridesAfterPreset(t *testing.T) {
	o := defaultOptions()
	PresetK8s(K8sOpts{Dir: "/p", ProfileEnv: "PROF"})(&o)
	WithStrict(false)(&o)
	if o.strict {
		t.Error("explicit WithStrict(false) after preset must win")
	}
	if o.dir != "/p" || o.profileEnv != "PROF" {
		t.Errorf("preset args not honoured: dir=%q env=%q", o.dir, o.profileEnv)
	}
}

func TestPresetSidecarHistory(t *testing.T) {
	o := defaultOptions()
	PresetSidecar(SidecarOpts{})(&o)
	if o.historyCap != 16 {
		t.Errorf("historyCap=%d want 16", o.historyCap)
	}
	if o.dir != "/etc/fastconfd" {
		t.Errorf("dir=%q want /etc/fastconfd", o.dir)
	}
}

type cfg124 struct {
	App struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"app"`
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
