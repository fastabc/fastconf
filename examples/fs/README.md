# Load one embedded file

From the repository root, with Go 1.24+:

```sh
go run ./examples/fs
```

Expected output:

```text
host=localhost port=9090
```

[main.go](main.go) embeds [app.yaml](app.yaml), passes
`source.NewFS(files, "app.yaml")` to `fastconf.WithProvider`, and loads a typed
configuration with unknown keys rejected and port validation enabled.
`WithFS(files)` confines directory discovery to the embedded filesystem; the
single-file provider contributes the actual configuration document.

`source.NewFS` was added after v1.1.0. It infers the codec from the filename,
requires the file to exist, and defaults to a 4 MiB limit. Its priority allows
environment and CLI providers to override embedded defaults. It has no automatic
watcher; an embedded document remains unchanged for the lifetime of the binary.

`make example` runs this program. See [embedded files and cloud sources](../../docs/cookbook/sources/cloud.md).
