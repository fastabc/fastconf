package fastconf

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// TenantManager[T] is the multi-tenancy facade. It owns a registry of
// fully independent Manager[T] instances keyed by tenant id, so
// different tenants may have different providers, profiles, validators,
// or feature flags while sharing one process and one reader-side API.
//
// Design choices:
//
//   - Each tenant gets its own goroutine-safe Manager[T]; there is no
//     cross-tenant coupling, which keeps the failure-isolation
//     guarantee per tenant. A bad provider in tenant A cannot stall
//     reloads in tenant B.
//   - Get(tenant) is a single map lookup behind a RWMutex; the read
//     side is intentionally O(1) on the steady state.
//   - Add returns the underlying Manager so callers can subscribe,
//     plan, or close it directly. Remove is idempotent.
//
// TenantManager does NOT proxy options across tenants — the caller
// supplies the full options slice for each Add call. This keeps the
// public API tiny and avoids "spooky action at a distance" where a
// shared option would surprisingly affect every tenant.
type TenantManager[T any] struct {
	mu       sync.RWMutex
	tenants  map[string]*Manager[T]
	closed   bool
}

// NewTenantManager constructs an empty registry. Tenants are added
// via Add. The zero value is NOT usable — always go through this
// constructor so future fields can be initialised.
func NewTenantManager[T any]() *TenantManager[T] {
	return &TenantManager[T]{tenants: map[string]*Manager[T]{}}
}

// ErrTenantExists is returned by Add when the tenant id is already
// registered. Callers must Remove the prior instance first if they
// want to swap configuration atomically.
var ErrTenantExists = errors.New("fastconf: tenant already registered")

// ErrUnknownTenant is returned by Get/Remove for ids that were never
// added. Use Has() if you need a check that does not allocate or
// surface an error.
var ErrUnknownTenant = errors.New("fastconf: unknown tenant")

// Add constructs and registers a Manager[T] for tenant id. The
// supplied options are passed to New verbatim. The framework
// automatically wraps the user's AuditSink so every emitted
// ReloadCause carries Tenant=id, eliminating boilerplate at the
// call site.
func (tm *TenantManager[T]) Add(ctx context.Context, id string, opts ...Option) (*Manager[T], error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty tenant id", ErrUnknownTenant)
	}
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return nil, errors.New("fastconf: TenantManager closed")
	}
	if _, ok := tm.tenants[id]; ok {
		tm.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrTenantExists, id)
	}
	tm.mu.Unlock()

	// Build the manager outside the lock so a slow upstream provider
	// can't block other tenants from being read.
	wrapped := append(opts, withTenantTag(id))
	m, err := New[T](ctx, wrapped...)
	if err != nil {
		return nil, fmt.Errorf("tenant %q: %w", id, err)
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.closed {
		_ = m.Close()
		return nil, errors.New("fastconf: TenantManager closed during Add")
	}
	if _, ok := tm.tenants[id]; ok {
		// Race: another Add for the same id won. Close ours, return
		// the registered one with ErrTenantExists.
		_ = m.Close()
		return tm.tenants[id], fmt.Errorf("%w: %q", ErrTenantExists, id)
	}
	tm.tenants[id] = m
	return m, nil
}

// Get returns the manager for id; ErrUnknownTenant if absent.
func (tm *TenantManager[T]) Get(id string) (*Manager[T], error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	m, ok := tm.tenants[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTenant, id)
	}
	return m, nil
}

// Has returns true when id has been Added and not yet Removed.
func (tm *TenantManager[T]) Has(id string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	_, ok := tm.tenants[id]
	return ok
}

// Remove closes the underlying Manager and de-registers id. It is
// safe to call Remove for an unknown tenant (returns ErrUnknownTenant
// without side effects).
func (tm *TenantManager[T]) Remove(id string) error {
	tm.mu.Lock()
	m, ok := tm.tenants[id]
	if !ok {
		tm.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrUnknownTenant, id)
	}
	delete(tm.tenants, id)
	tm.mu.Unlock()
	return m.Close()
}

// Tenants returns a snapshot of the currently registered ids in
// unspecified order. The returned slice is safe to mutate.
func (tm *TenantManager[T]) Tenants() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]string, 0, len(tm.tenants))
	for id := range tm.tenants {
		out = append(out, id)
	}
	return out
}

// Close closes every registered Manager and marks the registry as
// closed. Subsequent Add calls will fail. Close aggregates errors
// using errors.Join.
func (tm *TenantManager[T]) Close() error {
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return nil
	}
	tm.closed = true
	mgrs := make([]*Manager[T], 0, len(tm.tenants))
	for _, m := range tm.tenants {
		mgrs = append(mgrs, m)
	}
	tm.tenants = nil
	tm.mu.Unlock()
	var joined error
	for _, m := range mgrs {
		joined = errors.Join(joined, m.Close())
	}
	return joined
}

// withTenantTag is an internal Option that stamps every emitted
// ReloadCause with Tenant=id. We achieve this without modifying the
// reload pipeline by wrapping every registered AuditSink.
func withTenantTag(id string) Option {
	return func(o *options) {
		wrapped := make([]AuditSink, 0, len(o.auditSinks)+1)
		for _, s := range o.auditSinks {
			wrapped = append(wrapped, tenantAuditSink{inner: s, tenant: id})
		}
		// Even with no user sink we still want the Tenant tag visible
		// to downstream code that introspects State.Cause; install a
		// no-op sink so a future WithAuditSink layered on top sees a
		// tagged copy too.
		if len(wrapped) == 0 {
			wrapped = append(wrapped, tenantAuditSink{inner: nil, tenant: id})
		}
		o.auditSinks = wrapped
	}
}

type tenantAuditSink struct {
	inner  AuditSink
	tenant string
}

func (s tenantAuditSink) Audit(ctx context.Context, cause ReloadCause) error {
	cause.Tenant = s.tenant
	if s.inner == nil {
		return nil
	}
	return s.inner.Audit(ctx, cause)
}
