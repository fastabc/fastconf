# Local multi-tenant configuration

Run from the repository root (Go 1.24+, no database connection is made):

```sh
go run ./examples/tenant
FC_TENANT_A_DATABASE_POOL=12 go run ./examples/tenant
go run ./examples/tenant -dir /absolute/path/to/conf.d
```

The program reads editable local files, creates two managers, prints masked
diagnostics, rejects an invalid tenant-a override, and updates tenant-b.
Without `FC_TENANT_A_*` / `FC_TENANT_B_*` environment variables:

```text
tenant-a addr=:8081 timeout=30s pool=10 rps=100
tenant-a dsn=***REDACTED***
tenant-b addr=:8082 timeout=30s pool=20 rps=200
tenant-b dsn=***REDACTED***
tenant-a rejected=true preserved=true tenant-b untouched=true
tenant-b updated rps=250 tenant-a rps=100
```

## Complete local files

```text
conf.d/
  base/00-app.yaml                 # shared defaults
  overlays/prod/50-app.yaml        # shared production settings
  tenants/tenant-a.yaml            # tenant-a only
  tenants/tenant-b.yaml            # tenant-b only
```

| File | Configuration |
|---|---|
| [base/00-app.yaml](conf.d/base/00-app.yaml) | Address `:8080`, timeout `5s`, pool `5`, RPS `50` |
| [overlays/prod/50-app.yaml](conf.d/overlays/prod/50-app.yaml) | Timeout `30s`, pool `20` |
| [tenants/tenant-a.yaml](conf.d/tenants/tenant-a.yaml) | Address `:8081`, tenant-a DSN, pool `10`, RPS `100` |
| [tenants/tenant-b.yaml](conf.d/tenants/tenant-b.yaml) | Address `:8082`, tenant-b DSN, RPS `200`; inherits pool `20` |

For example, tenant-a's entire local document is:

```yaml
server:
  addr: ":8081"
database:
  dsn: "postgres://demo:demo@localhost/tenant_a"
  pool: 10
limits:
  rps: 100
```

Copy the whole `conf.d` directory and pass `-dir` to use your own files. Edit
`tenants/tenant-a.yaml` and rerun to see only tenant-a change. Database URLs
contain demonstration credentials; `fc:"secret"` masks DSNs in diagnostic views,
while business code can read plaintext through `Get()`.

## Selection and precedence

Each manager explicitly registers its own `source.NewFile` and env prefix:

```text
shared base < shared prod overlay < tenant file < tenant environment < one-shot override
```

All providers sit above directory layers, including a `source.NewFile` with
default static priority. `tenants/` is not an automatically scanned overlay:
only the file registered for that manager is loaded. `WithTenant(id)` stamps
causes and policy input; it does not filter files, environment or credentials.

`FC_TENANT_A_DATABASE_POOL=12` overrides only tenant-a. The registry contains
fixed trusted IDs; validate IDs before using external input in file paths.
The example requires each tenant file at startup because `source.NewFile`
otherwise treats missing files as empty optional layers.

## Reload and lifecycle

The demonstration performs synchronous `Reload` calls and exits. Its invalid
override leaves tenant-a's snapshot and generation unchanged; tenant-b uses its
own writer and can continue reloading. Overrides last for one reload and do not
edit YAML. An ordinary reload restores values from files and environment.

For a long-running service, add `fastconf.WithWatch(fastconf.Watch{})` to each
manager and keep it alive until application shutdown. The shared watcher tracks
both `WithDir` layers and tenant file paths, including atomic replacements.
Shared-file changes can affect both managers; one tenant's file cannot load its
contents into the other. Commits are independent across managers.

The registry is built once and closed by its owner, including partial startup
failure. Concurrent tenant insertion or eviction needs synchronized registry
access and rules for in-flight users. Do not share mutable providers or mutable
hook state between tenant managers.

```sh
go test -race ./examples/tenant
```

Related: [tenant cookbook](../../docs/cookbook/tenant.md),
[reload failures](../../docs/cookbook/reload-policy.md),
[secrets](../../docs/cookbook/secrets.md).
