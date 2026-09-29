package contracts

import "context"

// Generator produces encoded layers during assembly. It runs on the reload goroutine:
// implementations must honor cancellation and bound their runtime. Errors abort the reload and
// preserve the previous snapshot.
type Generator interface {
	// Name identifies the generator in diagnostics. Each source path is
	// gen://<Name>/<RawLayer.Name>; an empty layer name is not replaced.
	Name() string
	// Generate returns the synthetic layers contributed for this reload.
	// Returning a nil slice and a nil error is a valid "nothing to add"
	// outcome.
	Generate(ctx context.Context) ([]RawLayer, error)
}

// RawLayer is a named encoded document contributed by a Generator. Priority orders generator
// layers (higher wins); declaration order breaks ties.
type RawLayer struct {
	Name     string
	Codec    string
	Data     []byte
	Priority int
}
