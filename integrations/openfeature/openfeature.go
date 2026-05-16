// Package openfeature adapts a FastConf Manager into an
// OpenFeature-shaped provider. The intent is that downstream projects
// already wired to the OpenFeature SDK can swap their ConfigCat /
// LaunchDarkly / Unleash provider for FastConf without changing the
// call sites that read flags.
//
// This package intentionally does NOT import
// github.com/open-feature/go-sdk — pulling in the full OpenFeature
// SDK would force every user to take on that closure even when they
// only want native FastConf APIs. Instead we ship:
//
//   - EvaluationContext (a simple map[string]string alias matching
//     the OpenFeature spec's required Targeting attributes)
//   - Resolution structs (Bool / String / Int / Float) carrying Value
//     and a Reason string
//   - Provider, whose method signatures mirror the OpenFeature
//     "FeatureProvider" interface
//
// A real OpenFeature SDK adapter is a 20-line shim that wraps
// Provider and converts our Resolution types to the SDK's
// ResolutionDetail types. See README cookbook for the example.
package openfeature

import (
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/feature"
)

// EvaluationContext mirrors OpenFeature's evaluation-context shape.
// It is a flat string→string map; downstream callers can adapt their
// SDK's EvaluationContext into this form trivially.
type EvaluationContext map[string]string

// Reason names mirror the OpenFeature spec's Reason field.
const (
	ReasonTargetingMatch = "TARGETING_MATCH"
	ReasonDefault        = "DEFAULT"
	ReasonError          = "ERROR"
	ReasonStatic         = "STATIC"
)

// BoolResolutionDetail is the OpenFeature-shaped result for boolean
// evaluations.
type BoolResolutionDetail struct {
	Value  bool
	Reason string
}

// StringResolutionDetail is the result for string evaluations.
type StringResolutionDetail struct {
	Value  string
	Reason string
}

// IntResolutionDetail is the result for integer evaluations.
type IntResolutionDetail struct {
	Value  int64
	Reason string
}

// FloatResolutionDetail is the result for float evaluations.
type FloatResolutionDetail struct {
	Value  float64
	Reason string
}

// Evaluator is the minimal Manager-facing API the adapter needs. Users
// typically construct it via FromManager; tests can supply a stub.
type Evaluator interface {
	Eval(key string, ctx feature.EvalContext, def any) any
}

// FromManager wraps a fastconf.Manager[T] into an Evaluator that resolves
// flag look-ups against the live State[T]'s feature rule table.
func FromManager[T any](m *fastconf.Manager[T]) Evaluator {
	return managerEvaluator[T]{m: m}
}

type managerEvaluator[T any] struct {
	m *fastconf.Manager[T]
}

func (e managerEvaluator[T]) Eval(key string, ctx feature.EvalContext, def any) any {
	if e.m == nil {
		return def
	}
	s := e.m.Snapshot()
	if s == nil {
		return def
	}
	return feature.Eval(s.FeatureRules(), key, ctx, def)
}

// Provider implements the OpenFeature FeatureProvider contract on top
// of an Evaluator (typically constructed via FromManager).
type Provider struct {
	M       Evaluator
	KeyRoot string // optional prefix prepended to every flag key
}

// New constructs a Provider routing flag look-ups to m. keyRoot is
// optional; non-empty values are joined to flag names with a dot
// ("features"+"."+name).
func New(m Evaluator, keyRoot string) *Provider {
	return &Provider{M: m, KeyRoot: keyRoot}
}

func (p *Provider) key(flag string) string {
	if p.KeyRoot == "" {
		return flag
	}
	return p.KeyRoot + "." + flag
}

// BooleanEvaluation evaluates a boolean flag.
func (p *Provider) BooleanEvaluation(flag string, def bool, ec EvaluationContext) BoolResolutionDetail {
	if p == nil || p.M == nil {
		return BoolResolutionDetail{Value: def, Reason: ReasonError}
	}
	v := p.M.Eval(p.key(flag), feature.EvalContext(ec), def)
	if b, ok := v.(bool); ok {
		if b == def {
			return BoolResolutionDetail{Value: b, Reason: ReasonStatic}
		}
		return BoolResolutionDetail{Value: b, Reason: ReasonTargetingMatch}
	}
	return BoolResolutionDetail{Value: def, Reason: ReasonDefault}
}

// StringEvaluation evaluates a string flag.
func (p *Provider) StringEvaluation(flag, def string, ec EvaluationContext) StringResolutionDetail {
	if p == nil || p.M == nil {
		return StringResolutionDetail{Value: def, Reason: ReasonError}
	}
	v := p.M.Eval(p.key(flag), feature.EvalContext(ec), def)
	if s, ok := v.(string); ok {
		reason := ReasonTargetingMatch
		if s == def {
			reason = ReasonStatic
		}
		return StringResolutionDetail{Value: s, Reason: reason}
	}
	return StringResolutionDetail{Value: def, Reason: ReasonDefault}
}

// IntEvaluation evaluates an integer flag.
func (p *Provider) IntEvaluation(flag string, def int64, ec EvaluationContext) IntResolutionDetail {
	if p == nil || p.M == nil {
		return IntResolutionDetail{Value: def, Reason: ReasonError}
	}
	v := p.M.Eval(p.key(flag), feature.EvalContext(ec), def)
	switch n := v.(type) {
	case int:
		return IntResolutionDetail{Value: int64(n), Reason: ReasonTargetingMatch}
	case int64:
		return IntResolutionDetail{Value: n, Reason: ReasonTargetingMatch}
	case float64:
		return IntResolutionDetail{Value: int64(n), Reason: ReasonTargetingMatch}
	}
	return IntResolutionDetail{Value: def, Reason: ReasonDefault}
}

// FloatEvaluation evaluates a floating-point flag.
func (p *Provider) FloatEvaluation(flag string, def float64, ec EvaluationContext) FloatResolutionDetail {
	if p == nil || p.M == nil {
		return FloatResolutionDetail{Value: def, Reason: ReasonError}
	}
	v := p.M.Eval(p.key(flag), feature.EvalContext(ec), def)
	switch n := v.(type) {
	case float64:
		return FloatResolutionDetail{Value: n, Reason: ReasonTargetingMatch}
	case int:
		return FloatResolutionDetail{Value: float64(n), Reason: ReasonTargetingMatch}
	case int64:
		return FloatResolutionDetail{Value: float64(n), Reason: ReasonTargetingMatch}
	}
	return FloatResolutionDetail{Value: def, Reason: ReasonDefault}
}
