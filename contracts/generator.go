package contracts

import "context"

// Generator dynamically synthesises one or more RawLayer carriers
// during the assemble stage of a reload. Unlike a Provider, which
// returns a single map[string]any, a Generator can produce multiple
// layers with distinct codecs and priorities — useful for injecting
// build-info / shell-out / K8s downward-api / sealed-secret style
// data.
//
// Generators run on the single reload goroutine, so implementations
// must respect ctx cancellation and bound their own runtime
// (e.g. timeouts for shell-out generators). Returning an error aborts
// the reload (failure-safe — previous *State[T] is preserved).
type Generator interface {
	// Name is used for diagnostics and RawLayer.Name when the generator
	// does not stamp its own.
	Name() string
	// Generate returns the synthetic layers contributed for this reload.
	// Returning a nil slice and a nil error is a valid "nothing to add"
	// outcome.
	Generate(ctx context.Context) ([]RawLayer, error)
}
