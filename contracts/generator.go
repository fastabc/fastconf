package contracts

import "context"

// Generator produces encoded layers during assembly. It runs on the reload goroutine:
// implementations must honor cancellation and bound their runtime. Errors abort the reload and
// preserve the previous snapshot.
type Generator interface {
	// Name is used for diagnostics and RawLayer.Name when the generator
	// does not stamp its own.
	Name() string
	// Generate returns the synthetic layers contributed for this reload.
	// Returning a nil slice and a nil error is a valid "nothing to add"
	// outcome.
	Generate(ctx context.Context) ([]RawLayer, error)
}
