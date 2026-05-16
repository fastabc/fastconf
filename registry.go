package fastconf

// Provider factory registry: name -> factory[contracts.Provider]. Lets
// WithProviderByName("vault", cfg) declaratively wire providers from
// YAML/CLI without compile-time imports.
//
// The framework cannot compile-time import every third-party provider
// (some live in optional submodules; some are user code). The
// registry pattern decouples the provider catalog from the framework
// core: providers register a constructor at init time, and users
// reference providers by short name through configuration, e.g.
// loaded from YAML / CLI / Kustomize meta:
//
//	# _meta.yaml
//	spec:
//	  providers:
//	    - name: vault
//	      config: { addr: "https://vault.svc", path: "kv/data/app" }
//
// The constructor receives the per-instance config map and returns a
// contracts.Provider. Errors at construction time are returned to the
// caller (Manager.New); they do not panic.
//
// Two registries coexist:
//
//   - A process-wide default (populated by package-level
//     RegisterProviderFactory) — convenient for single-tenant apps
//     where vault/consul/http register from init().
//   - Per-Manager registries (NewProviderRegistry + WithProviderRegistry)
//     — isolated state for multi-tenant tests and sub-systems that
//     must not see each other's factories.
//
// When WithProviderByName resolves, the per-Manager registry (if any)
// is consulted first, then the global default. Resolution is deferred
// until all Options have been applied so the registry can be installed
// in any order.

import (
	"fmt"
	"sort"
	"sync"

	"github.com/fastabc/fastconf/contracts"
)

// ProviderFactory builds a Provider from a free-form config map. The
// map shape is provider-specific; a vault factory might look for
// "addr" and "path", an HTTP factory for "url" etc. Factories MUST
// validate the config and return an error rather than panic.
type ProviderFactory func(cfg map[string]any) (contracts.Provider, error)

// ProviderRegistry is an explicit, instance-scoped map of named
// ProviderFactory entries. Use NewProviderRegistry to construct one and
// WithProviderRegistry(r) to attach it to a Manager.
//
// The zero value is NOT usable; always call NewProviderRegistry.
// Methods are safe for concurrent use.
type ProviderRegistry struct {
	mu sync.RWMutex
	m  map[string]ProviderFactory
}

// NewProviderRegistry returns an empty registry. Pair with
// WithProviderRegistry to scope provider lookups to a single Manager
// (or TenantManager tenant) instead of the process-wide global.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{m: map[string]ProviderFactory{}}
}

// Register adds a named factory. Re-registering an existing name
// overwrites the previous factory; tests rely on this.
func (r *ProviderRegistry) Register(name string, f ProviderFactory) {
	if r == nil || name == "" || f == nil {
		return
	}
	r.mu.Lock()
	r.m[name] = f
	r.mu.Unlock()
}

// Lookup returns a registered factory and whether it existed.
func (r *ProviderRegistry) Lookup(name string) (ProviderFactory, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	f, ok := r.m[name]
	r.mu.RUnlock()
	return f, ok
}

// Names returns the sorted list of registered factory names. Useful
// for diagnostic output (e.g. "have: [vault consul http]").
func (r *ProviderRegistry) Names() []string {
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

// defaultProviderRegistry backs the package-level Register / Lookup /
// RegisteredProviderNames functions so existing init()-based factory
// registration keeps working unchanged.
var defaultProviderRegistry = NewProviderRegistry()

// RegisterProviderFactory adds a named factory to the process-wide
// default registry. Safe to call from init() across packages.
// Re-registering an existing name overwrites — useful for test fakes.
//
// For test isolation or multi-tenant setups, prefer NewProviderRegistry
// + WithProviderRegistry instead of mutating the global.
func RegisterProviderFactory(name string, f ProviderFactory) {
	defaultProviderRegistry.Register(name, f)
}

// LookupProviderFactory consults only the process-wide default registry.
// Exposed for diagnostic tooling such as `fastconfctl`.
func LookupProviderFactory(name string) (ProviderFactory, bool) {
	return defaultProviderRegistry.Lookup(name)
}

// RegisteredProviderNames returns the sorted list of process-wide
// factory names. Per-Manager registries are not included; ask the
// registry instance directly via (*ProviderRegistry).Names() when
// debugging an isolated setup.
func RegisteredProviderNames() []string {
	return defaultProviderRegistry.Names()
}

// pendingByName records a deferred WithProviderByName lookup. New()
// resolves these after every Option has applied so the lookup sees the
// final per-Manager registry regardless of Option order.
type pendingByName struct {
	name string
	cfg  map[string]any
}

// WithProviderRegistry installs a Manager-local ProviderRegistry. When
// set, WithProviderByName resolves names against this registry first,
// then falls back to the process-wide default.
//
// Use cases:
//   - Multi-tenant: each tenant has its own factory set without
//     touching the global registry.
//   - Tests: install fakes for a single test without race-y mutation
//     of process state.
//   - Plugin sandboxing: a sub-system can declare exactly which
//     providers it allows to be wired in by configuration.
func WithProviderRegistry(r *ProviderRegistry) Option {
	return func(o *options) { o.providerRegistry = r }
}

// WithProviderByName resolves a provider through the registry and
// installs it. It is the dynamic counterpart to WithProvider, useful
// when the provider list comes from configuration rather than code.
//
// Resolution is deferred until all Options have been applied, so the
// per-Manager registry (WithProviderRegistry) may appear in any order
// relative to WithProviderByName.
//
// Missing factory names and factory errors are recorded as deferred
// option errors; New() surfaces them before starting any goroutine.
func WithProviderByName(name string, cfg map[string]any) Option {
	return func(o *options) {
		o.pendingByName = append(o.pendingByName, pendingByName{name: name, cfg: cfg})
	}
}

// resolveProvidersByName converts every recorded pendingByName entry
// into a real Provider attached via WithProvider semantics. Called from
// New() once all Options have been applied.
//
// The lookup order is: Manager-local registry → process-wide default.
// Misses and constructor errors append to o.deferredErrs.
func (o *options) resolveProvidersByName() {
	for _, p := range o.pendingByName {
		f, ok := o.lookupProviderFactory(p.name)
		if !ok {
			o.deferredErrs = append(o.deferredErrs,
				fmt.Errorf("%w: provider factory %q not registered (have: %v)",
					ErrFastConf, p.name, o.knownProviderNames()))
			continue
		}
		pr, err := f(p.cfg)
		if err != nil {
			o.deferredErrs = append(o.deferredErrs,
				fmt.Errorf("%w: provider %q: %v", ErrFastConf, p.name, err))
			continue
		}
		WithProvider(pr)(o)
	}
	o.pendingByName = nil
}

// lookupProviderFactory consults the Manager-local registry first, then
// the process-wide default.
func (o *options) lookupProviderFactory(name string) (ProviderFactory, bool) {
	if o.providerRegistry != nil {
		if f, ok := o.providerRegistry.Lookup(name); ok {
			return f, true
		}
	}
	return defaultProviderRegistry.Lookup(name)
}

// knownProviderNames returns the union of local + default registry
// names for diagnostic error messages.
func (o *options) knownProviderNames() []string {
	seen := map[string]struct{}{}
	for _, n := range defaultProviderRegistry.Names() {
		seen[n] = struct{}{}
	}
	if o.providerRegistry != nil {
		for _, n := range o.providerRegistry.Names() {
			seen[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
