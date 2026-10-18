# Load one file

From the repository root, with Go 1.24+:

```sh
go run ./examples/single-file
go run ./examples/single-file -file /path/to/app.yaml
```

The bundled [app.yaml](app.yaml) prints `port=8080`. [main.go](main.go) is the
complete program: `Load` returns a State, `Value` returns the typed configuration,
and there is no Manager to close. Unknown keys and ports outside 1–65535 fail.
A missing source file is an empty layer, so the port check also rejects it.

The file extension selects the parser. JSON struct tags work with YAML input;
select `WithDecodeFormat(fastconf.YAML)` if the struct uses only YAML tags.
Keep the value and reachable objects read-only.

The default directory loader also looks for `conf.d` in the working directory.
This repository-root example has no such directory. In an application with
existing layers, choose its directory explicitly with `WithDir`, or run from a
dedicated directory to keep this a single-source example.

See [the loading guide](../../docs/getting-started/load.md).
