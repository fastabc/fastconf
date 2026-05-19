package external_source_test

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/parser"
	"github.com/fastabc/fastconf/pkg/source"
)

type externalSourceExampleConfig struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Feature struct {
		BetaEnabled bool `yaml:"betaEnabled" json:"betaEnabled"`
	} `yaml:"feature" json:"feature"`
}

// staticExampleProvider implements contracts.Provider directly because
// its data is already structured (no bytes → decode required). The Source
// contract is the alternative used by byte-blob layers (see seed below).
type staticExampleProvider struct {
	name     string
	priority int
	data     map[string]any
}

func (p *staticExampleProvider) Name() string  { return p.name }
func (p *staticExampleProvider) Priority() int { return p.priority }

func (p *staticExampleProvider) Load(context.Context) (map[string]any, error) {
	out := make(map[string]any, len(p.data))
	for k, v := range p.data {
		out[k] = v
	}
	return out, nil
}

func (p *staticExampleProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

// Example_externalSource demonstrates the two complementary extension
// points for fastconf:
//
//   - WithSource(Source, Parser) for byte-blob layers (file / http /
//     inline bytes) where the decoder is named at the call site;
//   - WithProvider for already-structured contributors (env / cli /
//     KV with one key per setting).
//
// See also: docs/cookbook/cross-process.md — wiring real NATS / Redis
// Streams providers via the same WithProvider entry point.
func Example_externalSource() {
	demo := &staticExampleProvider{
		name:     "demo-static",
		priority: contracts.PriorityKV,
		data: map[string]any{
			"server":  map[string]any{"addr": ":9090"},
			"feature": map[string]any{"betaEnabled": true},
		},
	}

	// Inline byte-blob layer paired with an explicit Parser. To let
	// `demo` (PriorityKV=30) supply the override, push the seed below
	// it via WithPriority.
	seed := source.NewBytes("seed", "yaml",
		[]byte("server:\n  addr: \":8080\"\nfeature:\n  betaEnabled: false\n"),
	).WithPriority(contracts.PriorityStatic)

	mgr, err := fastconf.New[externalSourceExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")},
		}),
		fastconf.WithSource(seed, parser.YAML()),
		fastconf.WithProvider(demo),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	app := mgr.Get()
	fmt.Printf("%s %t\n", app.Server.Addr, app.Feature.BetaEnabled)
	// Output:
	// :9090 true
}
