// Package feature evaluates configuration-backed feature flags using ordered targets and
// deterministic percentage rollouts, without a remote service.
package feature

import (
	"crypto/sha256"
	"encoding/binary"
)

// EvalContext supplies request attributes for targeting and rollout hashing.
type EvalContext map[string]string

// Rule is a configuration-backed flag with defaults, targets and rollouts.
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

// Target matches when every key in When is present in the evaluation context
// with the same value. A missing key differs from a present empty string.
// An empty When never matches.
type Target struct {
	When  map[string]string `json:"when" yaml:"when"`
	Value any               `json:"value" yaml:"value"`
}

// Rollout deterministically buckets a context attribute into a 0-99 space. When HashKey is missing
// from ctx the rollout is skipped (it cannot decide deterministically without an anchor).
type Rollout struct {
	Percent int    `json:"percent" yaml:"percent"`
	HashKey string `json:"hashKey" yaml:"hashKey"`
	Value   any    `json:"value" yaml:"value"`
}

// Evaluate returns Value for the first matching Target or Rollout, or Default when nothing
// matches. Evaluation is pure and deterministic for the same (Rule, ctx) pair.
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

// matches requires a non-empty target whose attributes all match the context.
func matches(want, have EvalContext) bool {
	if len(want) == 0 {
		return false
	}
	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// inBucket returns true when the SHA-256 of anchor (mod 100) falls below percent. percent is
// clamped to [0, 100]. The hash is taken on the raw bytes; callers who want salting can prepend a
// namespace.
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

// Eval evaluates a named rule, returning def when the key is missing.
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
