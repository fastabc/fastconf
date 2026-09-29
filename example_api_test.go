package fastconf_test

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

type apiExampleConfig struct {
	Server struct {
		Addr string `json:"addr" yaml:"addr"`
	} `json:"server" yaml:"server"`
}

// ExampleNew demonstrates the shortest typed entry path: construct a manager,
// read the live value, and close it when the owner shuts down.
func ExampleNew() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-app.yaml": &fstest.MapFile{
				Data: []byte("server:\n  addr: \":8080\"\n"),
			},
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	fmt.Println(mgr.Get().Server.Addr)
	// Output:
	// :8080
}

// ExampleSubscribe demonstrates reacting to a typed subtree after a successful
// commit. Subscribe fires only when the extracted value actually changes;
// callers no longer need an inline equality check.
func ExampleSubscribe() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-app.yaml": &fstest.MapFile{
				Data: []byte("server:\n  addr: \":8080\"\n"),
			},
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	cancel := fastconf.Subscribe(mgr,
		func(c *apiExampleConfig) *string { return &c.Server.Addr },
		func(old, next *string) {
			fmt.Printf("%s -> %s\n", *old, *next)
		},
	)
	defer cancel()

	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	})); err != nil {
		fmt.Println(err)
		return
	}
	// Output:
	// :8080 -> :9090
}

// ExampleManager_Errors demonstrates the asynchronous failure stream that lets
// services centralize reload error handling without blocking the writer.
func ExampleManager_Errors() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-app.yaml": &fstest.MapFile{
				Data: []byte("server:\n  addr: \":8080\"\n"),
			},
		}),
		fastconf.WithValidate(func(c *apiExampleConfig) error {
			if c.Server.Addr == "" {
				return fmt.Errorf("server.addr is required")
			}
			return nil
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"server": map[string]any{"addr": ""},
	})); err == nil {
		fmt.Println("expected validation failure")
		return
	}
	re := <-mgr.Errors()
	fmt.Println(re.Reason, re.Err != nil)
	// Output:
	// override true
}

// ExampleManager_Plan demonstrates previewing a file-backed change before it
// becomes the live snapshot.
func ExampleManager_Plan() {
	files := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {addr: ':8080'}\n")},
	}

	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithFS(files),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	files["conf.d/base/00-app.yaml"].Data = []byte("server: {addr: ':9090'}\n")
	plan, err := mgr.Plan(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(len(plan.Diff), plan.Proposed.Value().Server.Addr, mgr.Get().Server.Addr)
	// Output:
	// 1 :9090 :8080
}

// ExampleHistory_Rollback demonstrates recovering a retained prior snapshot
// without rerunning the reload pipeline.
func ExampleHistory_Rollback() {
	files := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server: {addr: ':8080'}\n")},
	}

	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithFS(files),
		fastconf.WithHistory(2),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	files["conf.d/base/00-app.yaml"].Data = []byte("server: {addr: ':9090'}\n")
	if err := mgr.Reload(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	liveAfterReload := mgr.Get().Server.Addr
	history := mgr.History().List()
	if err := mgr.History().Rollback(history[0]); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(liveAfterReload, mgr.Get().Server.Addr)
	// Output:
	// :9090 :8080
}

// ExampleLoad_profiles demonstrates a one-shot load with a profile overlay.
func ExampleLoad_profiles() {
	type Config struct {
		Server struct {
			Addr string `json:"addr"`
		} `json:"server"`
		Database struct {
			Pool int `json:"pool"`
		} `json:"database"`
	}
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-app.yaml": &fstest.MapFile{
				Data: []byte("server: {addr: ':8080'}\ndatabase: {pool: 10}\n"),
			},
			"conf.d/overlays/prod/10-app.yaml": &fstest.MapFile{
				Data: []byte("server: {addr: ':8443'}\ndatabase: {pool: 32}\n"),
			},
		}),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	app := state.Value()
	fmt.Println(app.Server.Addr, app.Database.Pool, len(state.Sources()))
	// Output:
	// :8443 32 2
}

type externalSourceExampleConfig struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Feature struct {
		BetaEnabled bool `yaml:"betaEnabled" json:"betaEnabled"`
	} `yaml:"feature" json:"feature"`
}

// staticExampleProvider contributes an immutable structured map. Byte documents
// use providers/source, which decodes them before returning a snapshot.
type staticExampleProvider struct {
	name     string
	priority int
	data     map[string]any
}

func (p *staticExampleProvider) Name() string { return p.name }
func (p *staticExampleProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

func (p *staticExampleProvider) Load(context.Context) (contracts.Snapshot, error) {
	// The provider owns this map; FastConf copies it before merging.
	return contracts.Snapshot{Map: p.data}, nil
}

func (p *staticExampleProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}

// ExampleWithProvider combines an inline YAML document with an already-structured
// provider. Higher provider priorities override lower ones.
func ExampleWithProvider() {
	demo := &staticExampleProvider{
		name:     "demo-static",
		priority: contracts.PriorityKV,
		data: map[string]any{
			"server":  map[string]any{"addr": ":9090"},
			"feature": map[string]any{"betaEnabled": true},
		},
	}

	// Inline documents default to PriorityStatic (10), below PriorityKV (30).
	seed := source.NewBytes("seed", "yaml",
		[]byte("server:\n  addr: \":8080\"\nfeature:\n  betaEnabled: false\n"),
	)

	mgr, err := fastconf.New[externalSourceExampleConfig](context.Background(),
		fastconf.WithFS(fstest.MapFS{}), // this example uses only providers
		fastconf.WithProvider(seed, demo),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = mgr.Close() }()

	app := mgr.Get()
	fmt.Printf("%s %t\n", app.Server.Addr, app.Feature.BetaEnabled)
	// Output:
	// :9090 true
}
