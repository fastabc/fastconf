package fastconf

import (
	iopts "github.com/fastabc/fastconf/internal/options"
	"github.com/fastabc/fastconf/internal/registry"
)

type ProviderFactory = registry.Factory
type ProviderRegistry = registry.Registry

func NewProviderRegistry() *ProviderRegistry { return registry.New() }

// RegisterProviderFactory registers a named provider constructor in the
// global default registry. If a factory with the same name was already
// registered, it is silently overwritten. Call this from a package-level
// init() function to make the provider available to WithProviderByName.
func RegisterProviderFactory(name string, f ProviderFactory) {
	registry.Default.Register(name, f)
}

func LookupProviderFactory(name string) (ProviderFactory, bool) {
	return registry.Default.Lookup(name)
}

func RegisteredProviderNames() []string {
	return registry.Default.Names()
}

func unregisterProviderFactory(name string) { registry.Default.Unregister(name) }

func WithProviderRegistry(r *ProviderRegistry) Option {
	return func(o *options) { o.ProviderRegistry = r }
}

func WithProviderByName(name string, cfg map[string]any) Option {
	return func(o *options) {
		o.PendingByName = append(o.PendingByName, iopts.PendingByName{Name: name, Cfg: cfg})
	}
}
