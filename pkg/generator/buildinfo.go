// Package generator hosts FastConf's built-in contracts.Generator
// implementations. Generators synthesise configuration layers
// dynamically at assemble time. They are first-class peers of
// Provider but produce []contracts.RawLayer instead of one
// map[string]any.
package generator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// BuildInfo emits a single JSON Source containing the supplied
// dotted.key=value pairs. Typical use: stamp `app.version`,
// `app.commit`, and `app.builtAt` at link time into the config tree so
// downstream services can serve /healthz with build provenance.
//
// Example:
//
//	fastconf.WithGenerator(&generator.BuildInfo{
//	    Keys: map[string]string{
//	        "app.version": Version,
//	        "app.commit":  Commit,
//	    },
//	})
type BuildInfo struct {
	// NameStr overrides Name(); defaults to "buildinfo".
	NameStr string
	// Keys map dotted paths to string values.
	Keys map[string]string
}

// Name implements contracts.Generator.
func (b *BuildInfo) Name() string {
	if b.NameStr != "" {
		return b.NameStr
	}
	return "buildinfo"
}

// Generate implements contracts.Generator.
func (b *BuildInfo) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	if len(b.Keys) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for k, v := range b.Keys {
		mappath.Set(out, strings.Split(k, "."), v)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return []contracts.RawLayer{{
		Name:  "info",
		Codec: "json",
		Data:  data,
	}}, nil
}
