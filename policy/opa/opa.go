// Package opa adapts an OPA Rego policy bundle to fastconf.policy.Policy.
//
// This is a Phase 23 thin wrapper: the heavy
// github.com/open-policy-agent/opa dependency stays out of the core
// module and lives here in a dedicated Go submodule. Users who do
// not need OPA pay no compile-time or binary-size cost.
//
// Usage:
//
//	rego := `package fastconf
//	deny[msg] { input.config.debug == true; input.config.profile == "prod"; msg := "no debug in prod" }`
//	p, err := opa.New[MyApp]("no-debug-in-prod", rego)
//	if err != nil { ... }
//	mgr, _ := fastconf.New[MyApp](ctx, fastconf.WithPolicy(p))
package opa

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fastabc/fastconf/policy"
	"github.com/open-policy-agent/opa/rego"
)

// Policy[T] is an OPA-backed implementation of policy.Policy[T].
// Each Evaluate call serialises the *T to JSON, binds it to
// input.config, and runs the prepared query. Any "deny[msg]" rule
// that fires becomes a SeverityError Violation.
type Policy[T any] struct {
	name  string
	query rego.PreparedEvalQuery
}

// New compiles the supplied Rego module. The query is fixed at
// `data.fastconf.deny` to keep the public API tiny; users wanting a
// different entry point can wrap NewWithQuery.
func New[T any](name, module string) (*Policy[T], error) {
	return NewWithQuery[T](name, module, "data.fastconf.deny")
}

// NewWithQuery is the explicit form. The query MUST yield a string
// or array of strings; each element becomes one Violation.
func NewWithQuery[T any](name, module, query string) (*Policy[T], error) {
	q, err := rego.New(
		rego.Query(query),
		rego.Module("policy.rego", module),
	).PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("opa: prepare: %w", err)
	}
	return &Policy[T]{name: name, query: q}, nil
}

func (p *Policy[T]) Name() string { return p.name }

func (p *Policy[T]) Evaluate(ctx context.Context, in policy.Input[T]) ([]policy.Violation, error) {
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return nil, fmt.Errorf("opa: marshal config: %w", err)
	}
	var cfg any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("opa: unmarshal config: %w", err)
	}
	rs, err := p.query.Eval(ctx, rego.EvalInput(map[string]any{
		"config": cfg,
		"reason": in.Reason,
		"tenant": in.Tenant,
	}))
	if err != nil {
		return nil, fmt.Errorf("opa: eval: %w", err)
	}
	var out []policy.Violation
	for _, r := range rs {
		for _, e := range r.Expressions {
			switch v := e.Value.(type) {
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok {
						out = append(out, policy.Violation{Rule: p.name, Message: s, Severity: policy.SeverityError})
					}
				}
			case string:
				out = append(out, policy.Violation{Rule: p.name, Message: v, Severity: policy.SeverityError})
			}
		}
	}
	return out, nil
}
