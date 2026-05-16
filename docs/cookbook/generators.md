# Generators (Kustomize-style ConfigMap/Secret/Env generators)

`WithProvider(provider.NewBytes(...))` and `WithDotEnvAuto(prefix)` already inject ad-hoc layers, but they hard-code the producer in the call site. `WithGenerator` formalises the same idea so third-party producers can register a stable `Generate` API.

A `Generator` produces zero or more `contracts.Source` values during the `assemble` stage, after file discovery and before providers run. Failure aborts the reload and preserves the previous state.

## Interface

```go
type Generator interface {
    Name() string
    Generate(ctx context.Context) ([]contracts.Source, error)
}
```

## Example: build-info generator

`pkg/generator.BuildInfo` ships in-tree as a reference:

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/generator"
)

mgr, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithGenerator(&generator.BuildInfo{
        Keys: map[string]string{
            "app.version": "1.2.3",
            "app.commit":  "abc123",
        },
    }),
)
```

## Custom generators

Anything that conforms to `contracts.Generator` will work. Some patterns we expect downstream:

- **Shell generator**: shell out to `kubectl get configmap …` and parse stdout.
- **Downward API**: read `/etc/podinfo/labels` (mounted by Kubernetes) and emit a layer.
- **gRPC**: pull a tiny snapshot from an internal control-plane on every reload.

Generators MUST be deterministic for a given input — `Manager.Plan` invokes them, and a non-deterministic generator would make plan output unreproducible.

## Generator vs Transformer vs Provider

| Concept | When it runs | What it does |
|---------|-------------|--------------|
| `Generator` | `assemble` | produces new layers |
| `Provider` | `assemble` | also produces new layers but participates in watch and Resumable |
| `Transformer` | `transform` | mutates the merged map *after* all layers have been combined |

Use a Generator when the input is **static for the duration of a reload** but expensive enough to want a typed registration point.
