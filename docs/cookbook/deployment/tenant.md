# Multi-tenant configuration

Create one ordinary `Manager[T]` per tenant and keep it in your application's
registry. Each manager has its own snapshot, writer, history, error stream and
watchers. A failed reload in tenant-a preserves its last good snapshot while
tenant-b can continue. Isolation assumes extensions do not share mutable state
or serialize both managers through the same external resource.

`WithTenant(id)` labels `ReloadCause.Tenant` and `policy.Input.Tenant`. Select
files, environment prefixes and remote credentials explicitly for each manager.

## Run the complete local example

From the repository root:

```sh
go run ./examples/tenant
FC_TENANT_A_DATABASE_POOL=12 go run ./examples/tenant
```

[`examples/tenant`](../../../examples/tenant/README.md) contains the complete Go
program, editable local YAML, expected output and scenario tests. It uses no
external service and does not connect to a database.

The shared files are loaded with `WithDir` and an explicit prod profile.
Each manager adds exactly one tenant file and a distinct environment prefix:

```go
mgr, err := fastconf.New[Config](ctx,
    fastconf.WithDir(dir),
    fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
    fastconf.WithTenant("tenant-a"),
    fastconf.WithProvider(
        source.NewFile(filepath.Join(dir, "tenants", "tenant-a.yaml")),
        env.New("FC_TENANT_A_"),
    ),
    fastconf.WithUnknownFields(fastconf.UnknownError),
    fastconf.WithProvenance(fastconf.ProvenanceFull),
    fastconf.WithHistory(4),
)
if err != nil {
    return err
}
defer mgr.Close()
```

The precedence is shared base → prod overlay → tenant file → tenant env →
one-shot override. Static providers still sit above all directory layers.
FastConf does not automatically scan `tenants/`; a tenant-b document is never
registered with tenant-a. A required tenant file is checked before construction
because `source.NewFile` otherwise treats a missing file as an empty layer.

## Verify isolation

The example rejects `database.pool: 0` for tenant-a with `ErrInvalid`, verifies
that both managers' snapshots are unchanged, then updates only tenant-b's RPS.
`fc:"secret"` masks each DSN in diagnostic output. Tests also check separate env
overrides and startup failure when a tenant file is missing:

```sh
go test -race ./examples/tenant
```

## Use local files in a service

Copy the entire example `conf.d` and run with `-dir /absolute/path/to/conf.d`.
Add `WithWatch(Watch{})` and keep the managers alive for automatic local reloads;
both directory layers and tenant `source.NewFile` watch paths are covered.
Shared-file changes can affect both managers, with independent commits. Tenant
file contents remain isolated even when both parent directories are watched.

Close all managers at application shutdown, including those created before a
later tenant fails startup. A dynamic registry needs access locking, eviction
and in-flight request ownership rules. Close an evicted manager after releasing
the registry lock and after its users have finished; callbacks may need registry
access. Tenant IDs used in paths must be trusted or validated.

Related: [reload failures and overrides](../runtime/reload-policy.md),
[secret resolution and redaction](../processing/secrets.md),
[configuration ownership](../../design/spec.md#configuration-and-ownership).
