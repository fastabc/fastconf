package fastconf

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/contracts"
)

// NewValidator wraps a Schema into a FastConf-compatible validator function
// suitable for passing to WithValidator[T]. When schema is nil the returned
// function reports an error so misconfiguration is surfaced early.
func NewValidator[T any](schema contracts.Schema) func(*T) error {
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
