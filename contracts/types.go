// Package contracts defines the stable provider, generator and codec interfaces. Public contracts
// follow semantic versioning; internal packages remain private.
package contracts

import "github.com/fastabc/fastconf/internal/fcerr"

// DefaultMaxBodyBytes is the default post-response size budget used by the built-in document
// readers.
const DefaultMaxBodyBytes int64 = 4 << 20

// ErrConfigTooLarge identifies a response that exceeded its configured body budget. Readers
// consume max+1 bytes so oversized input is rejected before decoding rather than truncated into a
// different configuration.
var ErrConfigTooLarge = fcerr.ErrTooLarge

// Codec decodes a document into a map. Implementations must be concurrent-safe and reject
// documents whose root is not an object.
type Codec interface {
	Decode(data []byte) (map[string]any, error)
}

// RawLayer is a named encoded document contributed by a Generator. Priority orders generator
// layers (higher wins); declaration order breaks ties.
type RawLayer struct {
	Name     string
	Codec    string
	Data     []byte
	Priority int
}

// Standard provider priorities; higher wins and declaration order breaks ties. Providers override
// file and generator layers, and precede one-shot overrides.
const (
	PriorityDotEnv = 5
	PriorityStatic = 10
	PriorityKV     = 30
	PriorityK8s    = 40
	PriorityEnv    = 50
	PriorityCLI    = 60
)
