package feature_test

import (
	"testing"

	"github.com/fastabc/fastconf/pkg/feature"
)

func TestRule_DefaultWhenNoMatch(t *testing.T) {
	r := feature.Rule{Default: false}
	if v := r.Evaluate(feature.EvalContext{"region": "us"}); v != false {
		t.Fatalf("got %v", v)
	}
}

func TestRule_TargetWins(t *testing.T) {
	r := feature.Rule{
		Default: false,
		Targets: []feature.Target{
			{When: feature.EvalContext{"region": "eu-west"}, Value: true},
		},
	}
	if v := r.Evaluate(feature.EvalContext{"region": "eu-west"}); v != true {
		t.Fatalf("eu-west should match: got %v", v)
	}
	if v := r.Evaluate(feature.EvalContext{"region": "us"}); v != false {
		t.Fatalf("us should fall through to default: got %v", v)
	}
}

func TestRule_TargetOrderFirstMatchWins(t *testing.T) {
	r := feature.Rule{
		Default: "x",
		Targets: []feature.Target{
			{When: feature.EvalContext{"tier": "gold"}, Value: "g"},
			{When: feature.EvalContext{"tier": "gold"}, Value: "g2"},
		},
	}
	if v := r.Evaluate(feature.EvalContext{"tier": "gold"}); v != "g" {
		t.Fatalf("first match must win: got %v", v)
	}
}

func TestRule_RolloutBucket(t *testing.T) {
	r := feature.Rule{
		Default:  false,
		Rollouts: []feature.Rollout{{Percent: 50, HashKey: "user.id", Value: true}},
	}
	hits := 0
	for i := 0; i < 1000; i++ {
		// crude distribution check: ~50% of users fall in
		anchor := []byte{byte(i % 256), byte(i / 256)}
		if v := r.Evaluate(feature.EvalContext{"user.id": string(anchor)}); v == true {
			hits++
		}
	}
	if hits < 400 || hits > 600 {
		t.Fatalf("rollout distribution outside tolerance: %d/1000", hits)
	}
}

func TestRule_RolloutZeroAndHundred(t *testing.T) {
	zero := feature.Rule{Default: false, Rollouts: []feature.Rollout{{Percent: 0, HashKey: "u", Value: true}}}
	full := feature.Rule{Default: false, Rollouts: []feature.Rollout{{Percent: 100, HashKey: "u", Value: true}}}
	if zero.Evaluate(feature.EvalContext{"u": "x"}) != false {
		t.Fatalf("percent=0 should always miss")
	}
	if full.Evaluate(feature.EvalContext{"u": "x"}) != true {
		t.Fatalf("percent=100 should always hit")
	}
}

func TestRule_RolloutMissingAnchor(t *testing.T) {
	r := feature.Rule{
		Default:  "def",
		Rollouts: []feature.Rollout{{Percent: 50, HashKey: "user.id", Value: "on"}},
	}
	if v := r.Evaluate(feature.EvalContext{}); v != "def" {
		t.Fatalf("missing anchor should skip rollout: got %v", v)
	}
}

func TestEval_MissingKeyReturnsDefault(t *testing.T) {
	rules := map[string]feature.Rule{"a": {Default: 1}}
	if v := feature.Eval(rules, "b", feature.EvalContext{}, "fallback"); v != "fallback" {
		t.Fatalf("missing key should return def: got %v", v)
	}
}
