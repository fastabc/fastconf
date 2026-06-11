package contracts

// Schema is the minimal contract a schema-style validator must satisfy.
// Implementations live in sub-modules so the parent fastconf module stays
// free of heavy dependencies (cuelang.org/go, go-playground/validator,
// jsonschema engines, etc.).
//
// ValidateJSON receives the canonical JSON encoding of the FastConf state and
// returns nil on success or a descriptive error on failure.
type Schema interface {
	ValidateJSON(data []byte) error
}
