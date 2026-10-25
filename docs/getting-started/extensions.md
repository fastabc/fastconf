# Extensions: add a source or policy

Start with [one-shot loading](load.md) or [dynamic management](dynamic.md), then
add the extensions your application needs. The local provider and `policy.Func`
examples below ship with the root module, require Go 1.24+, and need no external
services.

## Environment overrides

Complete program and configuration: [examples/basic](../../examples/basic/README.md).
It applies base → prod overlay → env, checking unknown fields and the port range.

```sh
go run ./examples/basic
FC_EXAMPLE_SERVER_PORT=9090 go run ./examples/basic
FC_EXAMPLE_SERVER_PORT=0 go run ./examples/basic
```

The first command prints the fixture's prod configuration, the second shows
port=9090, and the third returns a validation error. Import
`github.com/fastabc/fastconf/providers/env` and register
`fastconf.WithProvider(env.New("FC_EXAMPLE_"))`. Use a separate prefix for profile
selection variables so strict decoding does not treat them as unknown fields.

## Policy checks

Complete program: [examples/reload](../../examples/reload/README.md).
Import `github.com/fastabc/fastconf/policy` and add business rules with
`WithPolicy(policy.Func[Config]{...})`. Run `go run ./examples/reload` to see field
validation rejection, policy rejection, policy warnings, a successful commit,
rollback, and source-file repair.

Even when Plan returns a nil error, inspect Validators and Policies: warnings
allow publication, while SeverityError blocks it. Sources are read again between
Plan and Reload.

## Extension points and module boundaries

Extensions in the root module need no separate installation. Choose by use case:

| Need | Package / API | Notes |
|---|---|---|
| Extra files, `fs.FS`, bytes and environment variables | `providers/source`, `providers/env`, `providers/dotenv` | [Sources and layering recipes](../cookbook/README.md#sources-and-layering) |
| Explicit CLI arguments | `providers/cliflag` | `cliflag.New(values)` accepts a nested map without expanding dotted keys; the pflag adapter performs that conversion |
| Remote configuration | `providers/http`, `providers/vault`, `providers/consul` | Native watchers run independently of the file-watching option |
| Custom sources | `contracts.Provider`, optional `contracts.Watcher` / `Describer` | [Ownership and lifecycle](../design/spec.md#configuration-and-ownership) |
| Encoded document generation | `contracts.Generator` / `WithGenerator` | [Generators](../cookbook/processing/generators.md) |
| Post-merge transformations | `transform` / `WithTransform` | Environment substitution, field aliases, deletion and schema migration chains |
| Type conversion and business validation | `codec` / `WithTypedHook`, `WithValidate`, `policy.Func` | [Conversion and validation recipes](../cookbook/README.md#merge-conversion-and-validation) |
| File output | `integrations/render` | Generate configuration for file-based daemons; see below |

## Optional modules

Install only the adapters your application imports, for example:

```sh
go get github.com/fastabc/fastconf/providers/s3@v1.2.0
```

The table below helps locate modules. Each linked `go.mod` is authoritative for
its minimum Go version and dependencies; the root module's Go 1.24 minimum does
not apply to every adapter.

| Capability | Module / manifest |
|---|---|
| S3 objects and event adaptation | [`providers/s3`](../../providers/s3/go.mod); includes `s3events` |
| AWS Parameter Store | [`providers/parameterstore`](../../providers/parameterstore/go.mod); added in v1.2.0 |
| Google Cloud Storage | [`providers/gcs`](../../providers/gcs/go.mod); added in v1.2.0 |
| Azure Blob Storage | [`providers/azblob`](../../providers/azblob/go.mod); added in v1.2.0 |
| AWS AppConfig | [`providers/appconfig`](../../providers/appconfig/go.mod); added in v1.2.0 |
| Azure App Configuration | [`providers/azappconfig`](../../providers/azappconfig/go.mod); added in v1.2.0 |
| GCP Secret Manager | [`providers/secretmanager`](../../providers/secretmanager/go.mod); added in v1.2.0 |
| SNS through SQS | [`integrations/notify/sns`](../../integrations/notify/sns/go.mod); added in v1.2.0 |
| Azure Service Bus | [`integrations/notify/azservicebus`](../../integrations/notify/azservicebus/go.mod); added in v1.2.0 |
| GCP Pub/Sub | [`integrations/notify/pubsub`](../../integrations/notify/pubsub/go.mod); added in v1.2.0 |
| Explicit pflag arguments | [`integrations/cli/pflag`](../../integrations/cli/pflag/go.mod) |
| go-playground validation | [`validate/playground`](../../validate/playground/go.mod) |
| CUE validation and policies | [`cue`](../../cue/go.mod); includes `cuelang` and `policy` packages |
| OPA/Rego policies | [`policy/opa`](../../policy/opa/go.mod) |
| Prometheus metrics | [`observability/metrics/prometheus`](../../observability/metrics/prometheus/go.mod) |
| OpenTelemetry tracing | [`observability/otel`](../../observability/otel/go.mod) |
| zerolog logging | [`integrations/log/zerolog`](../../integrations/log/zerolog/go.mod) |
| phuslu/log logging | [`integrations/log/phuslu`](../../integrations/log/phuslu/go.mod) |
| YAML → Go struct generation | [`cmd/fastconfgen`](../../cmd/fastconfgen/go.mod) |

See [optional integration programs](../../examples/README.md#optional-integration-programs)
for local run commands, [embedded files and cloud sources](../cookbook/sources/cloud.md)
for client ownership, refresh behavior and size limits, and
[cloud notifications](../cookbook/sources/cloud-notifications.md) for SNS, Service
Bus and Pub/Sub setup.

Within this repository, `bash tools/modules.sh exec <command>` validates the
current source in a temporary workspace instead of using a published root
module. See [repository tooling](../../tools/README.md) for module discovery and
release rules.

## Configuration output for file-based daemons

`integrations/render.Wire` accepts a Manager, `Renderer[T]`, output path, options
and hooks. It subscribes and immediately renders the current snapshot. Output is
written to a temporary file in the same directory, then atomically renamed;
identical bytes skip the write. Render or write failures preserve the existing
file. A post-write hook failure does not roll back the file or configuration
snapshot.

The renderer, write and subsequent hooks run synchronously and can delay the
writer; bound external operations. Handle failures through `Options.OnError`.
Initial rendering failures also use that callback and may not be returned by
`Wire`. Call the returned cancel function to unsubscribe, and close the Manager
when the service exits. Full interfaces and contract tests are in
[`integrations/render`](../../integrations/render).
