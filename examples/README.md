# Examples

Run the following complete programs from the repository root with Go 1.24+.
These programs need no external services or credentials and follow the current
checkout. `single-file` uses `LoadFile`, added after v1.1.0. The linked READMEs
provide source/fixture links, expected output and verification commands.

## Loading and source conversion

| Example | Demonstrates | Command |
|---|---|---|
| [single-file](single-file/README.md) | One YAML file, strict keys, startup validation | `go run ./examples/single-file` |
| [dynamic](dynamic/README.md) | File watching, rejection and shutdown | `go run ./examples/dynamic` |
| [basic](basic/README.md) | Typed base/prod layering, duration decoding, env override and field validation | `go run ./examples/basic` |
| [ini](ini/README.md) | INI base/prod layering, typed scalars and preservation of leading zeros | `go run ./examples/ini` |

## Runtime changes and application behavior

| Example | Demonstrates | Command |
|---|---|---|
| [tenant](tenant/README.md) | Editable local files, shared defaults, isolated tenant files/env, redaction and failed reload isolation | `go run ./examples/tenant` |
| [resource-update](resource-update/README.md) | Bounded updates, failed connection retention and draining | `go run ./examples/resource-update` |
| [reload](reload/README.md) | Local file changes, Plan findings, rejection, commit and history rollback | `go run ./examples/reload` |
| [features](features/README.md) | Ordered targeting, deterministic 30% rollout and fallbacks | `go run ./examples/features` |

```sh
FC_EXAMPLE_SERVER_PORT=9090 go run ./examples/basic
FC_TENANT_A_DATABASE_POOL=12 go run ./examples/tenant
go test -race ./examples/...
```

`basic` and `features` embed YAML; `ini` embeds INI. `single-file`, `dynamic`
and `tenant` read local files; `reload` writes only to a temporary directory.
Use copies for watch/edit exercises and wait for each observed update before
writing the next one; filesystem coalescing can combine rapid writes.

## Executable API examples

Core API examples live in [example_api_test.go](../example_api_test.go).
Run them with `go test . -run '^Example' -v` from the repository root:

| Example | Demonstrates |
| --- | --- |
| `ExampleNew` / `ExampleLoad_profiles` | Live typed reads and one-shot profile overlay loading |
| `ExampleWithProvider` | Inline documents combined with a structured provider |
| `ExampleSubscribe` / `ExampleManager_Errors` | Change callbacks and reload failures |
| `ExampleManager_Plan` / `ExampleHistory_Rollback` | Preview and recovery |

## Transport reference implementations

[NATS](nats/README.md) and [Redis Streams](redisstream/README.md) are packages
with transport tests, rather than standalone commands. Copy and adapt them to
connect a real client. Their tests run with `go test ./examples/...`.

## Optional integration programs

The optional integration examples below use their adapter module's dependencies.
Run the commands from the repository root:

| Example | Demonstrates | Command |
| --- | --- | --- |
| [pflag](pflag/main.go) | Changed flags become nested configuration; defaults are omitted | `go -C integrations/cli/pflag run ../../../examples/pflag/main.go` |
| [s3](s3/main.go) | Load YAML from a local S3-compatible HTTP endpoint | `go -C providers/s3 run ../../examples/s3/main.go` |

These programs remain tracked in `examples/pflag/` and `examples/s3/`. They use
`//go:build ignore` so root tests and `go mod tidy` keep optional dependencies
isolated. `make example` explicitly runs both in a temporary workspace against
the local adapter modules; naming `main.go` bypasses the build constraint.
No AWS account, external service, or real credentials are needed. Both pflag
and S3 print a configuration map containing port 9090.

The adapter manifests select their declared root dependency. For validation
against this checkout, use the temporary workspace commands below; `make example`
runs these same optional programs:

```sh
bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
bash tools/modules.sh exec go -C providers/s3 run ../../examples/s3/main.go
```
