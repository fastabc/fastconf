# Multi-tenant configuration

`TenantManager[T]` is a registry of independent `Manager[T]` instances
keyed by tenant id. Failure isolation is per tenant: a flaky provider
in tenant A cannot stall reloads in tenant B.

```go
tm := fastconf.NewTenantManager[MyApp]()
defer tm.Close()

for _, t := range listTenants(ctx) {
    _, err := tm.Add(ctx, t.ID,
        fastconf.WithDir("/etc/myapp/tenants/"+t.ID),
        fastconf.WithProvider(vaultProvider(t)),
        fastconf.WithWatch(true),
    )
    if err != nil { log.Fatal(err) }
}

mgr, _ := tm.Get("acme")
cfg := mgr.Get()
```

Every emitted `ReloadCause` carries `Tenant=id` automatically — you
do not need to wire that into your audit sinks.
