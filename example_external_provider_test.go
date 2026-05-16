package fastconf_test

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"

	"github.com/fastabc/fastconf/pkg/provider"
)

type externalProviderExampleConfig struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Feature struct {
		BetaEnabled bool `yaml:"betaEnabled" json:"betaEnabled"`
	} `yaml:"feature" json:"feature"`
}

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

// Example_externalProvider demonstrates plugging a third-party provider into fastconf.
func Example_externalProvider() {
	demo := &staticExampleProvider{
		name:     "demo-static",
		priority: contracts.PriorityKV,
		data: map[string]any{
			"server":  map[string]any{"addr": ":9090"},
			"feature": map[string]any{"betaEnabled": true},
		},
	}

	// NewBytes default priority is 9000 (high); to let `demo` (PriorityKV=30)
	// supply the override, push the seed bytes below it.
	seed := provider.NewBytes("seed", "yaml", []byte("server:\n  addr: \":8080\"\nfeature:\n  betaEnabled: false\n")).
		WithPriority(contracts.PriorityStatic)

	mgr, err := fastconf.New[externalProviderExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")},
		}),
		fastconf.WithProvider(seed),
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
