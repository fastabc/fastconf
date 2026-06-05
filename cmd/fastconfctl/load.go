package main

import (
	"context"

	"github.com/fastabc/fastconf/cmd/internal/cli"
)

// loadDump constructs a manager from the supplied flags and returns the
// merged map (or empty map if the manager returned nil).
func loadDump(f cli.Flags) (map[string]any, error) {
	mgr, err := cli.LoadConfig[map[string]any](context.Background(), f)
	if err != nil {
		return nil, err
	}
	defer mgr.Close()
	v := mgr.Get()
	if v == nil {
		return map[string]any{}, nil
	}
	return *v, nil
}
