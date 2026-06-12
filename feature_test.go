package fastconf_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/feature"
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

// BenchmarkFeatureEval guards O1: the request-path Eval must not clone
// the whole rule table per call. With many rules the old cloning
// accessor allocated O(rules) per evaluation; the ref accessor keeps it
// allocation-free for value-typed defaults.
func BenchmarkFeatureEval(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("features:\n")
	for i := range 50 {
		fmt.Fprintf(&sb, "  flag%d:\n    default: false\n    targets:\n      - when: { region: eu }\n        value: true\n", i)
	}
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(sb.String())}}
	mgr, err := fastconf.New[cfg121](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithFeatureRules(func(c *cfg121) map[string]feature.Rule { return c.Features }),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()
	ctx := fastconf.EvalContext{"region": "us"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = fastconf.Eval(mgr, "flag25", ctx, false)
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
