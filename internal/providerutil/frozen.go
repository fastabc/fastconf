package providerutil

import (
	"io"

	"github.com/fastabc/fastconf/contracts"
)

// ReadAllMax is the compatibility entry point for contracts.ReadAllMax.
// Frozen: published sub-modules up to v1.2.x import this; keep the signature until v2.
func ReadAllMax(r io.Reader, max int64) ([]byte, error) { return contracts.ReadAllMax(r, max) }

// SafeError is the compatibility entry point for contracts.SafeError.
// Frozen: published sub-modules up to v1.2.x import this; keep the signature until v2.
func SafeError(message string, cause error) error { return contracts.SafeError(message, cause) }
