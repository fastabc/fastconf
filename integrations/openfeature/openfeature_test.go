package openfeature_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/integrations/openfeature"
	"github.com/fastabc/fastconf/pkg/feature"
)

type cfg struct {
	Features map[string]feature.Rule `json:"features"`
}

func newManager(t *testing.T, rulesYAML string) *fastconf.Manager[cfg] {
	t.Helper()
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(rulesYAML)},
	}
	m, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithFeatureRules[cfg](func(c *cfg) map[string]feature.Rule { return c.Features }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProvider_BooleanEvalTargetingMatch(t *testing.T) {
	yamlSrc := `
features:
  darkMode:
    default: false
    targets:
      - when: {region: "eu-west"}
        value: true
`
	mgr := newManager(t, yamlSrc)
	defer mgr.Close()

	// KeyRoot is empty: the extractor returns map keyed by short names.
	p := openfeature.New(openfeature.FromManager(mgr), "")
	got := p.BooleanEvaluation("darkMode", false, openfeature.EvaluationContext{"region": "eu-west"})
	if got.Value != true {
		t.Errorf("Value = %v, want true", got.Value)
	}
	if got.Reason != openfeature.ReasonTargetingMatch {
		t.Errorf("Reason = %v, want %v", got.Reason, openfeature.ReasonTargetingMatch)
	}
}

func TestProvider_BooleanEvalFallsBackToDefault(t *testing.T) {
	mgr := newManager(t, "features: {}\n")
	defer mgr.Close()

	p := openfeature.New(openfeature.FromManager(mgr), "")
	got := p.BooleanEvaluation("missing", true, nil)
	if got.Value != true {
		t.Errorf("Value = %v, want true (default)", got.Value)
	}
	if got.Reason == "" {
		t.Error("Reason should be set")
	}
}

func TestProvider_StringEvalTargetingMatch(t *testing.T) {
	yamlSrc := `
features:
  region:
    default: "default"
    targets:
      - when: {tier: "gold"}
        value: "premium"
`
	mgr := newManager(t, yamlSrc)
	defer mgr.Close()

	p := openfeature.New(openfeature.FromManager(mgr), "")
	got := p.StringEvaluation("region", "default", openfeature.EvaluationContext{"tier": "gold"})
	if got.Value != "premium" {
		t.Errorf("Value = %q, want premium", got.Value)
	}
}

func TestProvider_IntAndFloatEval(t *testing.T) {
	yamlSrc := `
features:
  cap:
    default: 10
    targets:
      - when: {plan: "pro"}
        value: 100
  rate:
    default: 0.1
    targets:
      - when: {plan: "pro"}
        value: 0.95
`
	mgr := newManager(t, yamlSrc)
	defer mgr.Close()
	p := openfeature.New(openfeature.FromManager(mgr), "")

	gotI := p.IntEvaluation("cap", 0, openfeature.EvaluationContext{"plan": "pro"})
	if gotI.Value != 100 {
		t.Errorf("IntEval = %v", gotI)
	}
	gotF := p.FloatEvaluation("rate", 0.0, openfeature.EvaluationContext{"plan": "pro"})
	if gotF.Value != 0.95 {
		t.Errorf("FloatEval = %v", gotF)
	}
}

func TestProvider_NilManagerReturnsError(t *testing.T) {
	p := openfeature.New(nil, "")
	got := p.BooleanEvaluation("anything", true, nil)
	if got.Reason != openfeature.ReasonError {
		t.Errorf("expected error reason for nil manager, got %q", got.Reason)
	}
}

func TestProvider_KeyRootPrependedToLookup(t *testing.T) {
	// Demonstrate KeyRoot's intent: when the rule table is keyed with a
	// flat dotted shape ("features.greeting"), KeyRoot lets the adapter
	// strip the namespacing burden from the caller.
	stub := stubEval{
		"features.greeting": "hello",
	}
	p := openfeature.New(stub, "features")
	got := p.StringEvaluation("greeting", "world", nil)
	if got.Value != "hello" {
		t.Errorf("with KeyRoot, value = %q", got.Value)
	}
}

// stubEval is a minimal Evaluator stand-in used by the KeyRoot test.
type stubEval map[string]any

func (s stubEval) Eval(key string, _ feature.EvalContext, def any) any {
	if v, ok := s[key]; ok {
		return v
	}
	return def
}
