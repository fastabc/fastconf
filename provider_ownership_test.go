package fastconf_test

// Provider map ownership: contracts.Provider.Load says the provider
// remains the owner of the returned map. These tests pin the assembly
// boundary clone that keeps pipeline stages (typed hooks, deep merge,
// secret resolve) from mutating provider-owned — or user-owned — maps.

import (
	"context"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/cliflag"
)

type ownershipCfg struct {
	Name string `json:"name"`
	// RPC must stay absent from the newFS base layers: the aliasing
	// under test only happens when the provider is the first layer to
	// introduce the subtree.
	RPC struct {
		Timeout time.Duration `json:"timeout"`
	} `json:"rpc"`
	DB struct {
		Host string `json:"host"`
	} `json:"db"`
}

// cachedMapProvider models providers (like cliflag.CLIProvider) that
// return the same long-lived map from every Load call.
type cachedMapProvider struct {
	name string
	prio int
	data map[string]any
}

func (p *cachedMapProvider) Name() string  { return p.name }
func (p *cachedMapProvider) Priority() int { return p.prio }
func (p *cachedMapProvider) Load(context.Context) (map[string]any, error) {
	return p.data, nil
}
func (p *cachedMapProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

func TestProviderMapNotMutatedByTypedHooks(t *testing.T) {
	orig := map[string]any{"rpc": map[string]any{"timeout": "30s"}}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(cliflag.NewCLI(orig)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().RPC.Timeout; got != 30*time.Second {
		t.Fatalf("timeout = %v", got)
	}
	// The duration hook rewrites "30s" → int64 in the merged tree; the
	// caller's map (aliased by cliflag) must keep the original string.
	if v, ok := orig["rpc"].(map[string]any)["timeout"].(string); !ok || v != "30s" {
		t.Errorf("provider map mutated by pipeline: %T(%v)",
			orig["rpc"].(map[string]any)["timeout"],
			orig["rpc"].(map[string]any)["timeout"])
	}
}

func TestProviderMapsDoNotCrossContaminate(t *testing.T) {
	lowMap := map[string]any{"db": map[string]any{"host": "low"}}
	highMap := map[string]any{"db": map[string]any{"host": "high"}}
	low := &cachedMapProvider{name: "low", prio: 100, data: lowMap}
	high := &cachedMapProvider{name: "high", prio: 200, data: highMap}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(low),
		fastconf.WithProvider(high),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().DB.Host; got != "high" {
		t.Fatalf("merge order: host = %q", got)
	}
	// Merging high's layer over low's aliased subtree must not write
	// high's value into low's own map.
	if v := lowMap["db"].(map[string]any)["host"]; v != "low" {
		t.Errorf("low provider map contaminated by higher layer: %v", v)
	}
}
