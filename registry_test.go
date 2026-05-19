package fastconf

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
)

func TestProviderRegistryRoundTrip(t *testing.T) {
	type cfg struct {
		Hello string `yaml:"hello"`
	}
	RegisterProviderFactory("memtest", func(map[string]any) (contracts.Provider, error) {
		return &memProvider{name: "memtest", data: map[string]any{"hello": "world"}}, nil
	})
	mgr, err := New[cfg](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")}}),
		WithProviderByName("memtest", nil),
	)
	if err != nil {
		t.Fatalf("New with registered provider: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Hello; got != "world" {
		t.Errorf("hello=%q want world", got)
	}
}

func TestProviderRegistryUnknownName(t *testing.T) {
	type cfg struct{}
	_, err := New[cfg](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")}}),
		WithProviderByName("nope-no-such", nil),
	)
	if err == nil {
		t.Fatal("expected error for unknown provider name")
	}
	if !errors.Is(err, ErrFastConf) {
		t.Errorf("error %v should chain to ErrFastConf", err)
	}
}

type memProvider struct {
	name string
	data map[string]any
}

func (p *memProvider) Name() string                                          { return p.name }
func (p *memProvider) Priority() int                                         { return 0 }
func (p *memProvider) Load(context.Context) (map[string]any, error)          { return p.data, nil }
func (p *memProvider) Watch(context.Context) (<-chan contracts.Event, error) { return nil, nil }

// TestProviderRegistry_ManagerLocal verifies T6: an isolated registry
// installed via WithProviderRegistry is consulted before the global
// default and works regardless of Option order. The global is NOT
// mutated, so other tests stay race-free.
func TestProviderRegistry_ManagerLocal(t *testing.T) {
	type cfg struct {
		Hello string `yaml:"hello"`
	}
	local := NewProviderRegistry()
	local.Register("scoped", func(map[string]any) (contracts.Provider, error) {
		return &memProvider{name: "scoped", data: map[string]any{"hello": "isolated"}}, nil
	})

	// Order intentionally inverted: WithProviderByName appears BEFORE
	// WithProviderRegistry. Deferred resolution must still see the
	// final registry state.
	mgr, err := New[cfg](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")}}),
		WithProviderByName("scoped", nil),
		WithProviderRegistry(local),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Hello; got != "isolated" {
		t.Errorf("hello=%q want isolated", got)
	}
	// And the global registry must NOT have been polluted.
	if _, ok := LookupProviderFactory("scoped"); ok {
		t.Error("manager-local register leaked to global default")
	}
}

// TestProviderRegistry_ManagerLocalOverridesGlobal verifies precedence:
// when the same name exists in both registries the local one wins.
func TestProviderRegistry_ManagerLocalOverridesGlobal(t *testing.T) {
	type cfg struct {
		Hello string `yaml:"hello"`
	}
	RegisterProviderFactory("dualname", func(map[string]any) (contracts.Provider, error) {
		return &memProvider{name: "dualname", data: map[string]any{"hello": "from-global"}}, nil
	})
	t.Cleanup(func() {
		// Drop the global registration so other tests do not race.
		// Drop the global registration so other tests do not race.
		unregisterProviderFactory("dualname")
	})

	local := NewProviderRegistry()
	local.Register("dualname", func(map[string]any) (contracts.Provider, error) {
		return &memProvider{name: "dualname", data: map[string]any{"hello": "from-local"}}, nil
	})

	mgr, err := New[cfg](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")}}),
		WithProviderRegistry(local),
		WithProviderByName("dualname", nil),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Hello; got != "from-local" {
		t.Errorf("hello=%q want from-local (manager-local must win over global)", got)
	}
}

// TestProviderRegistry_FallbackToGlobal asserts the local registry does
// not isolate names that *only* exist in the global — those still
// resolve, preserving zero-config init-time registration.
func TestProviderRegistry_FallbackToGlobal(t *testing.T) {
	type cfg struct {
		Hello string `yaml:"hello"`
	}
	RegisterProviderFactory("globalonly", func(map[string]any) (contracts.Provider, error) {
		return &memProvider{name: "globalonly", data: map[string]any{"hello": "global"}}, nil
	})
	t.Cleanup(func() {
		unregisterProviderFactory("globalonly")
	})

	mgr, err := New[cfg](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/.keep": &fstest.MapFile{Data: []byte("")}}),
		WithProviderRegistry(NewProviderRegistry()), // empty local
		WithProviderByName("globalonly", nil),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Hello; got != "global" {
		t.Errorf("hello=%q want global (fall-through to default registry expected)", got)
	}
}
