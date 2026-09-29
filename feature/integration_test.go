package feature_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/feature"
)

type featureConfig struct {
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
	mgr, err := fastconf.New[featureConfig](context.Background(), fastconf.WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	// Feature rules are an ordinary config field: evaluate them directly.
	rules := mgr.Get().Features
	if v := feature.Eval(rules, "darkMode", feature.EvalContext{"region": "eu-west"}, false); v != true {
		t.Fatalf("eu-west target should win: got %v", v)
	}
	if v := feature.Eval(rules, "darkMode", feature.EvalContext{"user.id": "anything"}, false); v != true {
		t.Fatalf("rollout=100 should hit: got %v", v)
	}
	if v := feature.Eval(rules, "darkMode", feature.EvalContext{"region": "us"}, false); v != false {
		t.Fatalf("us with no anchor should fall to default: got %v", v)
	}
	if v := feature.Eval(rules, "betaUI", nil, "fallback"); v != "off" {
		t.Fatalf("default should win: got %v", v)
	}
	if v := feature.Eval(rules, "missing", nil, "fallback"); v != "fallback" {
		t.Fatalf("missing key should return def: got %v", v)
	}
}

// BenchmarkFeatureEval guards the request path: evaluating a flag from the
// live snapshot must not clone the rule table per call.
func BenchmarkFeatureEval(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("features:\n")
	for i := range 50 {
		fmt.Fprintf(&sb, "  flag%d:\n    default: false\n    targets:\n      - when: { region: eu }\n        value: true\n", i)
	}
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(sb.String())}}
	mgr, err := fastconf.New[featureConfig](context.Background(), fastconf.WithFS(fs))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	ctx := feature.EvalContext{"region": "us"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = feature.Eval(mgr.Get().Features, "flag25", ctx, false)
	}
}
