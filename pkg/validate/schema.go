package validate

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Schema is the minimal contract a schema-style validator must satisfy.
// Implementations live in sub-modules so the parent fastconf module
// stays free of their heavy dependencies (cuelang.org/go,
// go-playground/validator, jsonschema engines, etc.).
//
// ValidateJSON receives the canonical JSON encoding of the FastConf
// state and returns nil on success or a descriptive error on failure
// (paths to offending fields recommended).
type Schema interface {
	ValidateJSON(data []byte) error
}

// NewValidator wraps a Schema into a FastConf-compatible validator
// function suitable for passing to fastconf.WithValidator[T]. When
// schema is nil the returned function always reports an error so
// misconfiguration is surfaced early instead of silently passing every
// reload.
func NewValidator[T any](schema Schema) func(*T) error {
	return func(t *T) error {
		if schema == nil {
			return errors.New("validate: nil schema")
		}
		if t == nil {
			return errors.New("validate: nil config")
		}
		data, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("validate: marshal: %w", err)
		}
		return schema.ValidateJSON(data)
	}
}
