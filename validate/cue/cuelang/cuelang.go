// Package cuelang provides a default cuelang.org/go-backed implementation
// of fastconf/pkg/validate.Schema. It compiles a CUE source string once at
// construction and unifies every JSON-encoded snapshot against it.
//
// The package lives in its own go.mod so projects that don't need CUE
// keep the cuelang.org/go transitive closure out of their build.
//
// Typical wiring:
//
//	sch, _ := cuelang.Compile("{ port: int & >0 & <65536 }")
//	mgr, _ := fastconf.New[Cfg](ctx,
//	    fastconf.WithValidator(validate.NewValidator[Cfg](sch)),
//	)
package cuelang

import (
	"errors"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/encoding/json"
)

// Schema is the cuelang.org/go-backed fastconf/pkg/validate.Schema.
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
