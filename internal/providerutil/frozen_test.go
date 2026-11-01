package providerutil

import "io"

// These entry points are imported by published v1 submodules.
var (
	_ func(io.Reader, int64) ([]byte, error) = ReadAllMax
	_ func(string, error) error              = SafeError
)
