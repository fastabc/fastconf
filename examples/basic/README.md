# Layered typed configuration

Run from the repository root (Go 1.24+):

```sh
go run ./examples/basic
FC_EXAMPLE_SERVER_PORT=9090 go run ./examples/basic
```

With no `FC_EXAMPLE_*` variables, the first command prints
`port=8080 timeout=30s pool=20`; the second changes only the port to 9090.

[`base/00-app.yaml`](conf.d/base/00-app.yaml) supplies initial values,
[`overlays/prod/50-app.yaml`](conf.d/overlays/prod/50-app.yaml) raises the timeout
and pool, and the environment provider wins last. Duration strings decode into
`time.Duration`; `fc` tags reject out-of-range ports and pool sizes.

Files are embedded so the program can run from any working directory. `Load`
returns a `State[Config]`; read the typed config with `state.Value()`. It starts
no workers. For editable files use `WithDir` without `WithFS`; for live reload
use `New`, `WithWatch(Watch{})`, and close the manager on shutdown.

Related: [environment conventions](../../docs/cookbook/env-replacer.md),
[runtime contract](../../docs/design/spec.md).
