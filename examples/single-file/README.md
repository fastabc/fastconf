# Load one file

From the repository root, with Go 1.24+:

```sh
go run ./examples/single-file
go run ./examples/single-file -file /path/to/app.yaml
```

The bundled [app.yaml](app.yaml) prints `port=8080`. [main.go](main.go) is the
complete program: `LoadFile` directly returns the typed configuration, and there
is no Manager to close. Unknown keys, ports outside 1–65535 and missing files fail.
`LoadFile` is new in the current checkout and is not part of the v1.1.0 release.

The file extension selects the parser; JSON struct tags map fields even with
YAML input. The file size limit is 4 MiB. The caller owns the returned value.

Only the named file is loaded; `conf.d` and profiles do not participate.
Use `Load` with options when you need a YAML struct bridge, additional providers,
custom validation or diagnostic snapshots; use `New` for runtime updates.

See [the loading guide](../../docs/getting-started/load.md).

Verify from the repository root: `go test -race ./examples/single-file`.
