# Multi-tenant configuration

Each tenant is an ordinary, fully isolated `Manager[T]` tagged with
`WithTenant(id)`. Failure isolation is per tenant: a flaky provider in tenant
A cannot stall reloads in tenant B. FastConf does not ship a tenant
container: most services already own a tenant registry with its own lazy
loading and eviction rules, so keep the managers there.

```go
type Tenants[T any] struct {
    mu   sync.RWMutex
    mgrs map[string]*fastconf.Manager[T]
}

func (ts *Tenants[T]) Add(ctx context.Context, id string, opts ...fastconf.Option) (*fastconf.Manager[T], error) {
    m, err := fastconf.New[T](ctx, append(opts, fastconf.WithTenant(id))...)
    if err != nil {
        return nil, fmt.Errorf("tenant %q: %w", id, err)
    }
    ts.mu.Lock()
    defer ts.mu.Unlock()
    if old, ok := ts.mgrs[id]; ok {
        _ = old.Close()
    }
    ts.mgrs[id] = m
    return m, nil
}

func (ts *Tenants[T]) Get(id string) (*fastconf.Manager[T], bool) {
    ts.mu.RLock()
    defer ts.mu.RUnlock()
    m, ok := ts.mgrs[id]
    return m, ok
}

func (ts *Tenants[T]) Close() error {
    ts.mu.Lock()
    defer ts.mu.Unlock()
    var errs error
    for id, m := range ts.mgrs {
        errs = errors.Join(errs, m.Close())
        delete(ts.mgrs, id)
    }
    return errs
}
```

Register tenants with their own sources:

```go
ts := &Tenants[MyApp]{mgrs: map[string]*fastconf.Manager[MyApp]{}}
defer ts.Close()
for _, t := range listTenants(ctx) {
    if _, err := ts.Add(ctx, t.ID,
        fastconf.WithDir("/etc/myapp/tenants/"+t.ID),
        fastconf.WithProvider(vaultProvider(t)),
    ); err != nil {
        log.Fatal(err)
    }
}
```

Every `ReloadCause` of a tagged manager carries `Tenant=id`, and policies see
it as `policy.Input.Tenant`, so audit sinks and policies need no extra wiring.
