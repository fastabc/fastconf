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
	return fastconf.Load[map[string]any](context.Background(), opts...)
}

func loadDump(f cli.Flags) (map[string]any, error) {
	st, err := loadSnapshot(f)
	if err != nil {
		return nil, err
	}
	return *st.Value(), nil
}
