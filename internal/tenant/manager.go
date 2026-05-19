package tenant

import (
	"context"
	"errors"
	"fmt"
	"sync"

	imanager "github.com/fastabc/fastconf/internal/manager"
	iopts "github.com/fastabc/fastconf/internal/options"
)

type Manager[T any] struct {
	mu      sync.RWMutex
	tenants map[string]*imanager.M[T]
	closed  bool
}

func New[T any]() *Manager[T] {
	return &Manager[T]{tenants: map[string]*imanager.M[T]{}}
}

var ErrTenantExists = errors.New("fastconf: tenant already registered")
var ErrUnknownTenant = errors.New("fastconf: unknown tenant")

func (tm *Manager[T]) Add(ctx context.Context, id string, opts ...iopts.Option) (*imanager.M[T], error) {
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

	wrapped := append(opts, withTenantTag(id))
	m, err := imanager.New[T](ctx, wrapped...)
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
		_ = m.Close()
		return tm.tenants[id], fmt.Errorf("%w: %q", ErrTenantExists, id)
	}
	tm.tenants[id] = m
	return m, nil
}

func (tm *Manager[T]) Get(id string) (*imanager.M[T], error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	m, ok := tm.tenants[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTenant, id)
	}
	return m, nil
}

func (tm *Manager[T]) Has(id string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	_, ok := tm.tenants[id]
	return ok
}

func (tm *Manager[T]) Remove(id string) error {
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

func (tm *Manager[T]) Tenants() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]string, 0, len(tm.tenants))
	for id := range tm.tenants {
		out = append(out, id)
	}
	return out
}

func (tm *Manager[T]) Close() error {
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return nil
	}
	tm.closed = true
	mgrs := make([]*imanager.M[T], 0, len(tm.tenants))
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

func withTenantTag(id string) iopts.Option {
	return func(o *iopts.Options) { o.Tenant = id }
}
