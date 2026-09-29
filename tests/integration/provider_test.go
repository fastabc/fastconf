package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/contracts/providertest"
	"github.com/fastabc/fastconf/providers/cliflag"
	"github.com/fastabc/fastconf/providers/env"
	"github.com/fastabc/fastconf/providers/source"
)

type loadOnlyProvider struct{ value atomic.Int64 }

func (*loadOnlyProvider) Name() string { return "load-only" }

func (p *loadOnlyProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"value": p.value.Load()}}, nil
}

func TestProvider_LoadOnlySupportsManualReload(t *testing.T) {
	p := &loadOnlyProvider{}
	p.value.Store(1)
	providertest.AssertProviderBasics(t, p)
	providertest.AssertWatchClosesOnCancel(t, p, time.Second)
	mgr, err := fastconf.New[map[string]int64](context.Background(),
		fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := (*mgr.Get())["value"]; got != 1 {
		t.Fatalf("initial value = %d, want 1", got)
	}
	mgr.Pause()
	p.value.Store(2)
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*mgr.Get())["value"]; got != 2 {
		t.Fatalf("reloaded value = %d, want 2", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mgr.Shutdown(ctx); err != nil {
		t.Fatalf("load-only provider prevented shutdown: %v", err)
	}
}

// TestNew_MissingBaseDirIsZeroFileLayers verifies that a provider-only manager
// must not need an on-disk conf.d/base.
func TestNew_MissingBaseDirIsZeroFileLayers(t *testing.T) {
	type cfg struct {
		Port int `json:"port"`
	}
	src := source.NewBytes("inline", "yaml", []byte("port: 7\n"))
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(src),
	)
	if err != nil {
		t.Fatalf("New without conf.d/base: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Port; got != 7 {
		t.Fatalf("port = %d; want 7", got)
	}

	if _, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fstest.MapFS{})); !errors.Is(err, fastconf.ErrNoSources) {
		t.Fatalf("no sources at all: err = %v; want ErrNoSources", err)
	}
	if _, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(src), fastconf.WithStrictMerge(true),
	); err == nil {
		t.Fatal("strict mode must still require the base directory")
	}
}

// TestSourceDefaultsRankBelowEnvAndCLI verifies that source.* defaults sit in the
// user priority range, so env and CLI keep overriding sourced documents.
func TestSourceDefaultsRankBelowEnvAndCLI(t *testing.T) {
	type cfg struct {
		Port int    `json:"port"`
		Host string `json:"host"`
	}
	path := filepath.Join(t.TempDir(), "extra.json")
	if err := os.WriteFile(path, []byte(`{"port":1,"host":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("F6TEST_PORT", "2")
	t.Setenv("F6TEST_HOST", "env")
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithProvider(source.NewBytes("inline", "json", []byte(`{"port":1,"host":"bytes"}`))),
		fastconf.WithProvider(env.New("F6TEST_")),
		fastconf.WithProvider(cliflag.New(map[string]any{"port": 3})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get(); got.Port != 3 || got.Host != "env" {
		t.Fatalf("got port=%d host=%q; want CLI port 3 and env host", got.Port, got.Host)
	}
}

func TestBytesProvider_OverridesFileLayers(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(source.NewBytes("override", "yaml", []byte("database:\n  dsn: from-bytes\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if mgr.Get().Database.DSN != "from-bytes" {
		t.Errorf("bytes source did not win: %q", mgr.Get().Database.DSN)
	}
	// pool still comes from base
	if mgr.Get().Database.Pool != 10 {
		t.Errorf("pool lost: %d", mgr.Get().Database.Pool)
	}
}

type portCfg struct {
	Port int `json:"port"`
}

// TestProvider_DescribePriorityOrdersLayers verifies: priority is
// provider metadata read through Describe, not a method on Provider.
func TestProvider_DescribePriorityOrdersLayers(t *testing.T) {
	low := &contractProvider{name: "low", prio: contracts.PriorityKV, port: 1}
	high := &contractProvider{name: "high", prio: contracts.PriorityEnv, port: 2}
	mgr, err := fastconf.New[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(high), fastconf.WithProvider(low))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Port; got != 2 {
		t.Fatalf("port = %d; want 2 from the Describe()-priority-50 provider", got)
	}
}

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

// cachedMapProvider models providers (like cliflag.Provider) that
// return the same long-lived map from every Load call.
type cachedMapProvider struct {
	name string
	prio int
	data map[string]any
}

func (p *cachedMapProvider) Name() string { return p.name }

func (p *cachedMapProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.prio}
}

func (p *cachedMapProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data}, nil
}

func TestProviderMapNotMutatedByTypedHooks(t *testing.T) {
	orig := map[string]any{"rpc": map[string]any{"timeout": "30s"}}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(cliflag.New(orig)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
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
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().DB.Host; got != "high" {
		t.Fatalf("merge order: host = %q", got)
	}
	// Merging high's layer over low's aliased subtree must not write
	// high's value into low's own map.
	if v := lowMap["db"].(map[string]any)["host"]; v != "low" {
		t.Errorf("low provider map contaminated by higher layer: %v", v)
	}
}

// snapshotProvider returns a revisioned snapshot.
type snapshotProvider struct {
	name string
	prio int
	rev  string
	data map[string]any
}

func (p *snapshotProvider) Name() string { return p.name }

func (p *snapshotProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.prio}
}

func (p *snapshotProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data, Revision: p.rev}, nil
}

func TestProviderRevisionReachesCause(t *testing.T) {
	p := &snapshotProvider{
		name: "etcdish",
		rev:  "rev-42",
		data: map[string]any{"db": map[string]any{"host": "ordered"}},
	}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(p),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	revs := mgr.Snapshot().Cause().Revisions
	if got := revs[p.name]; got != p.rev {
		t.Fatalf("revision lost: got %q, want %q (revisions=%v)",
			got, p.rev, revs)
	}
}
