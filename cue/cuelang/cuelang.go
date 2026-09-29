// Package cuelang provides a cuelang.org/go-backed schema validator. It
// compiles a CUE source string once at construction and unifies every
// JSON-encoded snapshot against it.
//
// The package lives in its own go.mod so projects that don't need CUE
// keep the cuelang.org/go transitive closure out of their build.
//
// Typical wiring:
//
//	sch, _ := cuelang.Compile("{ port: int & >0 & <65536 }")
//	mgr, _ := fastconf.New[Cfg](ctx,
//	    fastconf.WithValidate(cuelang.Validate[Cfg](sch)),
//	)
package cuelang

import (
	gojson "encoding/json"
	"errors"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/encoding/json"
)

// Schema is a compiled CUE schema.
type Schema struct {
	ctx    *cue.Context
	schema cue.Value
}

// Compile compiles a CUE source string into a Schema. The source must
// describe a single value (use a top-level struct literal, e.g.
// "{ port: int & >0 & <65536 }"); any compile error is returned.
func Compile(src string) (*Schema, error) {
	ctx := cuecontext.New()
	v := ctx.CompileString(src)
	if err := v.Err(); err != nil {
		return nil, fmt.Errorf("cuelang: compile: %w", err)
	}
	return &Schema{ctx: ctx, schema: v}, nil
}

// ValidateJSON unifies the JSON-encoded snapshot with the compiled
// schema and returns nil on success.
func (s *Schema) ValidateJSON(data []byte) error {
	if s == nil {
		return errors.New("cuelang: nil schema")
	}
	expr, err := json.Extract("snapshot.json", data)
	if err != nil {
		return fmt.Errorf("cuelang: extract json: %w", err)
	}
	v := s.ctx.BuildExpr(expr)
	if err := v.Err(); err != nil {
		return fmt.Errorf("cuelang: build: %w", err)
	}
	uni := s.schema.Unify(v)
	if err := uni.Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("cuelang: validate: %w", err)
	}
	return nil
}

// Validate adapts s into a typed validator for fastconf.WithValidate:
// the decoded *T is JSON-encoded (so json tags govern field names) and
// unified with the schema.
func Validate[T any](s *Schema) func(*T) error {
	return func(t *T) error {
		if t == nil {
			return errors.New("cuelang: nil config")
		}
		data, err := gojson.Marshal(t)
		if err != nil {
			return fmt.Errorf("cuelang: marshal: %w", err)
		}
		return s.ValidateJSON(data)
	}
}
