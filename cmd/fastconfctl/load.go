package main

import (
	"context"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

// loadSnapshot runs the pipeline once; one-shot queries need no reload loop.
func loadSnapshot(f cli.Flags, extra ...fastconf.Option) (*fastconf.State[map[string]any], error) {
	opts, err := f.Options(extra...)
	if err != nil {
		return nil, err
	}
	// A plaintext display redactor also restores historical Explain values and
	// Diff values; only fastconfctl opts in here, never the HTTP sidecar.
	if f.Unredacted {
		opts = append(opts, fastconf.WithRedactor(func(_ string, value any) any { return value }))
	}
	return fastconf.Load[map[string]any](context.Background(), opts...)
}
