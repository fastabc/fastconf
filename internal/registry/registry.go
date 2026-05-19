// Package registry holds the provider-factory registry that backs
// fastconf.WithProviderByName. The root package re-exports Factory and
// Registry via type aliases so the public API is unchanged; this package
// owns the storage, locking, and process-wide default singleton.
package registry

import (
	"sort"
	"sync"

	"github.com/fastabc/fastconf/contracts"
)

// Factory builds a Provider from a free-form config map. The map shape
// is provider-specific; a vault factory might look for "addr" and
// "path", an HTTP factory for "url" etc. Factories MUST validate the
// config and return an error rather than panic.
type Factory func(cfg map[string]any) (contracts.Provider, error)

// Registry is an explicit, instance-scoped map of named Factory
// entries. Use New to construct one. The zero value is NOT usable.
// Methods are safe for concurrent use.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Factory
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{m: map[string]Factory{}}
}

// Register adds a named factory. Re-registering an existing name
// overwrites the previous factory; tests rely on this.
func (r *Registry) Register(name string, f Factory) {
	if r == nil || name == "" || f == nil {
		return
	}
	r.mu.Lock()
	r.m[name] = f
	r.mu.Unlock()
}

// Unregister removes name from the registry. Idempotent; missing names
// are silently ignored. Primarily intended for tests that install a
// factory into the process-wide Default and need to drop it on
// t.Cleanup so other tests don't race.
func (r *Registry) Unregister(name string) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	delete(r.m, name)
	r.mu.Unlock()
}

// Lookup returns a registered factory and whether it existed.
func (r *Registry) Lookup(name string) (Factory, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	f, ok := r.m[name]
	r.mu.RUnlock()
	return f, ok
}

// Names returns the sorted list of registered factory names.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Default is the process-wide default registry. fastconf-root
// helpers RegisterProviderFactory / LookupProviderFactory /
// RegisteredProviderNames delegate to this singleton.
var Default = New()
