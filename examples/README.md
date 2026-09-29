# Examples

Core API examples live in [example_api_test.go](../example_api_test.go).
Run them with `go test . -run '^Example' -v` from the repository root:

| Example | Demonstrates |
| --- | --- |
| `ExampleNew` / `ExampleLoad_profiles` | Live typed reads and one-shot profile overlay loading |
| `ExampleWithProvider` | Inline documents combined with a structured provider |
| `ExampleSubscribe` / `ExampleManager_Errors` | Change callbacks and reload failures |
| `ExampleManager_Plan` / `ExampleHistory_Rollback` | Preview and recovery |

NATS and Redis Streams reference providers remain under `examples/`; their
transport tests run with `go test ./examples/...`.

The optional integration examples below use their adapter module's dependencies.
Run the commands from the repository root:

| Example | Demonstrates | Command |
| --- | --- | --- |
| `pflag` | Changed flags become nested configuration; defaults are omitted | `go -C integrations/cli/pflag run ../../../examples/pflag/main.go` |
| `s3` | Load YAML from a local S3-compatible HTTP endpoint | `go -C providers/s3 run ../../examples/s3/main.go` |

These programs use `//go:build ignore`: naming `main.go` explicitly runs them,
while root package tests and `go mod tidy` skip their optional dependencies.
No AWS account, external service, or real credentials are needed. Both pflag
and S3 print a configuration map containing port 9090.

Both adapters currently pin the unpublished v1.0.0 root module. To run these
programs against the checkout before publication, use the temporary workspace:

```sh
bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
bash tools/modules.sh exec go -C providers/s3 run ../../examples/s3/main.go
```
