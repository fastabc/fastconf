// Package cue adapts a CUE schema to fastconf.policy.Policy. The
// CUE evaluator validates the typed config against a constraint
// expression; any unification error becomes a SeverityError
// Violation.
//
// Like the OPA backend, this lives in its own Go submodule so the
// 30 MB cuelang.org/go transitive dependency is opt-in.
package cue

import (
	"context"
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/fastabc/fastconf/policy"
)

// Policy[T] runs a CUE schema against the typed config.
type Policy[T any] struct {
	name   string
	schema cue.Value
	cctx   *cue.Context
}

// New compiles the supplied CUE source. The compiled schema is
// reused across evaluations.
func New[T any](name, source string) (*Policy[T], error) {
	cctx := cuecontext.New()
	v := cctx.CompileString(source)
	if err := v.Err(); err != nil {
		return nil, fmt.Errorf("cue: compile: %w", err)
	}
	return &Policy[T]{name: name, schema: v, cctx: cctx}, nil
}

func (p *Policy[T]) Name() string { return p.name }

func (p *Policy[T]) Evaluate(_ context.Context, in policy.Input[T]) ([]policy.Violation, error) {
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return nil, fmt.Errorf("cue: marshal: %w", err)
	}
	cfg := p.cctx.CompileBytes(raw)
	if err := cfg.Err(); err != nil {
		return nil, fmt.Errorf("cue: load: %w", err)
	}
	unified := p.schema.Unify(cfg)
	if err := unified.Validate(cue.Concrete(true)); err != nil {
		return []policy.Violation{{
			Rule:     p.name,
			Message:  err.Error(),
			Severity: policy.SeverityError,
		}}, nil
	}
	return nil, nil
}
