// Package feature provides a tiny, allocation-light feature-flag /
// rollout evaluator that piggybacks on FastConf's strongly-typed
// configuration. Rules live in the same YAML / overlay tree as the
// rest of the config; the runtime evaluator (Rule.Evaluate) is a pure
// function over a map[string]string context, so it composes naturally
// with FastConf's lock-free read path.
//
// The design borrows the shape used by ConfigCat / LaunchDarkly /
// Unleash / OpenFeature: a default value, an ordered list of targeted
// overrides (first match wins), and an optional percentage rollout
// keyed on a context attribute. Unlike those SDKs, feature does not
// require a separate service — every rule is just a value in your
// config tree.
package feature

import (
	"crypto/sha256"
	"encoding/binary"
)

// EvalContext is the per-request bag of attributes used by Rule.Evaluate
// to pick targeted overrides or compute rollout buckets. Keep keys
// short and stable across services (e.g. "user.id", "region", "tier").
type EvalContext map[string]string

// Rule is one feature flag entry. Unmarshal it from YAML/JSON via the
// usual codec round-trip:
//
//	features:
//	  darkMode:
//	    default: false
//	    targets:
//	      - when: { region: "eu-west" }
//	        value: true
//	    rollouts:
//	      - percent: 30
//	        hashKey: "user.id"
//	        value: true
type Rule struct {
	// Key is the dotted name of this rule (e.g. "features.darkMode").
	// It is informational — Evaluate does not consult Key.
	Key string `json:"key,omitempty" yaml:"key,omitempty"`
	// Default is the value returned when no Target / Rollout matches.
	Default any `json:"default" yaml:"default"`
	// Targets are deterministic equality matches evaluated in order.
	// The first Target whose When clauses all match wins.
	Targets []Target `json:"targets,omitempty" yaml:"targets,omitempty"`
	// Rollouts evaluate in order after Targets. A request lands in a
	// rollout bucket when HashKey is present in ctx and its hash
	// modulo 100 falls below Percent.
	Rollouts []Rollout `json:"rollouts,omitempty" yaml:"rollouts,omitempty"`
}

// Target matches when every key/value pair in When equals the
// corresponding value in the evaluation context.
type Target struct {
	When  map[string]string `json:"when" yaml:"when"`
	Value any               `json:"value" yaml:"value"`
}

// Rollout deterministically buckets a context attribute into a 0-99
// space. When HashKey is missing from ctx the rollout is skipped (it
// cannot decide deterministically without an anchor).
type Rollout struct {
	Percent int    `json:"percent" yaml:"percent"`
	HashKey string `json:"hashKey" yaml:"hashKey"`
	Value   any    `json:"value" yaml:"value"`
}

// Evaluate returns Value for the first matching Target or Rollout, or
// Default when nothing matches. Evaluation is pure and deterministic
// for the same (Rule, ctx) pair.
func (r Rule) Evaluate(ctx EvalContext) any {
	for _, t := range r.Targets {
		if matches(t.When, ctx) {
			return t.Value
		}
	}
	for _, ro := range r.Rollouts {
		if ro.HashKey == "" {
			continue
		}
		anchor, ok := ctx[ro.HashKey]
		if !ok || anchor == "" {
			continue
		}
		if inBucket(anchor, ro.Percent) {
			return ro.Value
		}
	}
	return r.Default
}

// matches returns true when every key/value pair in want has an exact
// equality match in have. An empty want trivially matches anything.
func matches(want, have EvalContext) bool {
	if len(want) == 0 {
		return false
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// inBucket returns true when the SHA-256 of anchor (mod 100) falls
// below percent. percent is clamped to [0, 100]. The hash is taken on
// the raw bytes; callers who want salting can prepend a namespace.
func inBucket(anchor string, percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	h := sha256.Sum256([]byte(anchor))
	n := binary.BigEndian.Uint64(h[:8]) % 100
	return int(n) < percent
}

// Eval is the convenience entry point for evaluating a named rule from
// a rule table. Returns def when key is missing.
func Eval(rules map[string]Rule, key string, ctx EvalContext, def any) any {
	if rules == nil {
		return def
	}
	r, ok := rules[key]
	if !ok {
		return def
	}
	return r.Evaluate(ctx)
}
