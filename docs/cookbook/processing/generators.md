# Custom configuration generators

`WithGenerator` registers a producer of encoded layers. Use a provider for a
structured snapshot or native change notifications; use `source.NewBytes`
when the document is already available at construction.

A `Generator` produces zero or more `contracts.RawLayer` values during the `assemble` stage, after file discovery and before providers run. Failure aborts the reload and preserves the previous state.

## Interface

```go
type Generator interface {
    Name() string
    Generate(ctx context.Context) ([]contracts.RawLayer, error)
}
```

## Example: build-info generator

```go
import (
    "context"
    "encoding/json"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/contracts"
)

type BuildInfo struct {
    Version string
    Commit  string
}

func (b BuildInfo) Name() string { return "buildinfo" }

func (b BuildInfo) Generate(context.Context) ([]contracts.RawLayer, error) {
    data, err := json.Marshal(map[string]any{
        "app": map[string]any{
            "version": b.Version,
            "commit":  b.Commit,
        },
    })
    if err != nil {
        return nil, err
    }
    return []contracts.RawLayer{{
        Name:  "info",
        Codec: "json",
        Data:  data,
    }}, nil
}

mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithGenerator(BuildInfo{Version: "1.2.3", Commit: "abc123"}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

## Custom generators

FastConf defines the generator contract; these are application implementation
ideas, not built-in generators:

- **Shell generator**: shell out to `kubectl get configmap …` and parse stdout.
- **Downward API**: read `/etc/podinfo/labels` (mounted by Kubernetes) and emit a layer.
- **gRPC**: pull a tiny snapshot from an internal control-plane on every reload.

Keep output deterministic for a given input and honor context cancellation.
Plan also invokes generators, but inputs may change before Reload. Generators
have no watch channel: trigger Reload explicitly or through a separate watcher.

## Generator vs transform vs Provider

| Concept | When it runs | What it does |
|---------|-------------|--------------|
| `Generator` | `assemble` | produces new layers |
| `Provider` | `assemble` | loads a snapshot; optionally implements Watcher for notifications and resume |
| transform (`WithTransform`) | `transform` | mutates the merged map *after* all layers have been combined |
