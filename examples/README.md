# Examples

Run the following complete programs from the repository root with Go 1.24+.
These programs need no external services or credentials and follow the current
checkout. `single-file` and `fs` use APIs added in v1.2.0. The linked READMEs
provide source/fixture links, expected output and verification commands.

## Loading and source conversion

| Example | Demonstrates | Command |
|---|---|---|
| [single-file](single-file/README.md) | One YAML file, strict keys, startup validation | `go run ./examples/single-file` |
| [fs](fs/README.md) | One embedded YAML document through `source.NewFS`, typed loading and validation | `go run ./examples/fs` |
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
| [cloud-reload](cloud-reload/README.md) | Missed-notification recovery, periodic reload and consumer supervision | `go run ./examples/cloud-reload` |

```sh
FC_EXAMPLE_SERVER_PORT=9090 go run ./examples/basic
FC_TENANT_A_DATABASE_POOL=12 go run ./examples/tenant
go test -race ./examples/...
```

`basic`, `features` and `fs` embed YAML; `ini` embeds INI. `single-file`, `dynamic`
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
| [parameterstore](parameterstore/README.md) | Load two SSM pages into a nested string-valued tree | `go -C providers/parameterstore run ../../examples/parameterstore/main.go` |
| [gcs](gcs/README.md) | Load YAML and reuse a decoded generation | `go -C providers/gcs run ../../examples/gcs/main.go` |
| [azblob](azblob/README.md) | Load YAML and reuse cached data on HTTP 304 | `go -C providers/azblob run ../../examples/azblob/main.go` |
| [appconfig](appconfig/README.md) | Data API session and poll-interval cache | `go -C providers/appconfig run ../../examples/appconfig/main.go` |
| [azappconfig](azappconfig/README.md) | Prefix/label selection and nested string values | `go -C providers/azappconfig run ../../examples/azappconfig/main.go` |
| [secretmanager](secretmanager/README.md) | Resolve a version and verify CRC32C without printing secrets | `go -C providers/secretmanager run ../../examples/secretmanager/main.go` |
| [sns](sns/README.md) | SNS/S3 notification and Manager reload before SQS ACK | `go -C integrations/notify/sns run ../../../examples/sns/main.go` |
| [azservicebus](azservicebus/README.md) | Event Grid filtering and refresh before Complete | `go -C integrations/notify/azservicebus run ../../../examples/azservicebus/main.go` |
| [pubsub](pubsub/README.md) | Real Pub/Sub v2 SDK with local gRPC delivery and ACK | `go -C integrations/notify/pubsub run ../../../examples/pubsub/main.go` |

These optional programs use `//go:build ignore` so root tests and `go mod tidy`
keep SDK dependencies isolated. `make example` explicitly runs them in a
temporary workspace against the local adapter modules; naming `main.go` bypasses
the build constraint. No cloud account, external service or real credentials are
needed. Expected output is listed in each README. Cloud examples other than S3
were added in v1.2.0; use each module's `go.mod` for its minimum Go version.

The adapter manifests select their declared root dependency. For validation
against this checkout, use the temporary workspace commands below; `make example`
runs these same optional programs:

```sh
bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
bash tools/modules.sh exec go -C providers/s3 run ../../examples/s3/main.go
bash tools/modules.sh exec go -C providers/parameterstore run ../../examples/parameterstore/main.go
bash tools/modules.sh exec go -C providers/gcs run ../../examples/gcs/main.go
bash tools/modules.sh exec go -C providers/azblob run ../../examples/azblob/main.go
```
