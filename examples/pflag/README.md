# Typed CLI overrides with pflag

Run from the repository root using the optional module's Go version:

```sh
GOPROXY=direct bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
```

Expected output:

```text
host=file-host port=9090 debug=false
```

[main.go](main.go) converts only changed flags with `adapter.FromChanged`, wraps
them in `cliflag.New`, and layers them over a YAML document with `WithProvider`.
`Load[Config]` converts string values and validates required/range constraints.
The untouched CLI host default does not override `file-host`; explicit false
survives the `default:"true"` tag. Replace the deterministic argument list with
`os.Args[1:]` in an application. Use `UnknownError` for unknown struct keys.

See [environment and CLI conventions](../../docs/cookbook/sources/env-replacer.md).
