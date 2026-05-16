package fastconf_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/feature"
)

type cfg121 struct {
	Features map[string]feature.Rule `json:"features"`
}

func TestEval_TargetedAndRollout(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
features:
  darkMode:
    default: false
    targets:
      - when: { region: "eu-west" }
        value: true
    rollouts:
      - percent: 100
        hashKey: "user.id"
        value: true
  betaUI:
    default: "off"
`)},
	}
	mgr, err := fastconf.New[cfg121](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithFeatureRules(func(c *cfg121) map[string]feature.Rule {
			return c.Features
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if v := fastconf.Eval(mgr, "darkMode", fastconf.EvalContext{"region": "eu-west"}, false); v != true {
		t.Fatalf("eu-west target should win: got %v", v)
	}
	if v := fastconf.Eval(mgr, "darkMode", fastconf.EvalContext{"user.id": "anything"}, false); v != true {
		t.Fatalf("rollout=100 should hit: got %v", v)
	}
	if v := fastconf.Eval(mgr, "darkMode", fastconf.EvalContext{"region": "us"}, false); v != false {
		t.Fatalf("us with no anchor should fall to default: got %v", v)
	}
	if v := fastconf.Eval(mgr, "betaUI", nil, "fallback"); v != "off" {
		t.Fatalf("default should win: got %v", v)
	}
	if v := fastconf.Eval(mgr, "betaUI", nil, false); v != false {
		t.Fatalf("type mismatch should return typed default: got %v", v)
	}
	if v := fastconf.Eval(mgr, "missing", nil, "fallback"); v != "fallback" {
		t.Fatalf("missing key should return def: got %v", v)
	}
}

func TestEval_WithoutExtractor(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("features: {}")},
	}
	mgr, _ := fastconf.New[cfg121](context.Background(), fastconf.WithFS(fs))
	defer mgr.Close()
	if v := fastconf.Eval(mgr, "any", nil, "def"); v != "def" {
		t.Fatalf("without WithFeatureRules, Eval should return def: got %v", v)
	}
}
