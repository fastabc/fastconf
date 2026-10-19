# Documentation

These pages follow the current checkout. Additions after v1.1.0 are marked;
for a released dependency, read the same path at its installed tag. Commands
run from the repository root unless stated otherwise.

## Start here

| Need | Guide |
|---|---|
| Read one required file at startup | [Single file](getting-started/load.md); `LoadFile` was added after v1.1.0 |
| Load layers once | [Basic example](../examples/basic/README.md) with `Load` |
| Manage runtime updates | [Dynamic guide](getting-started/dynamic.md) with `New` |
| Add a source, validator or policy | [Extensions and optional modules](getting-started/extensions.md) |
| Run complete programs | [Examples](../examples/README.md) |
| Find a task-specific recipe | [Cookbook by function](cookbook/README.md) |
| Upgrade from v0.20.0 | [Migration to v1.1.0](cookbook/migration-v1.md) |

## Reference and development

| Document | Owns |
|---|---|
| [Runtime contract](design/spec.md) | Lifecycle, precedence, decoding, concurrency and ownership |
| [Architecture](architecture.md) | Package responsibilities and dependency direction |
| [Performance notes](design/perf.md) | Read-path budget, caches and measurement commands |
| [Repository tooling](../tools/README.md) | Development checks and module discovery |
| [Release guide](../RELEASING.md) | Coordinated module tags and distribution |
| [API reference](https://pkg.go.dev/github.com/fastabc/fastconf) | Published exported signatures and Go comments |

## Maintaining documentation

Keep each fact in one primary location: behavior in the runtime contract,
module choices in the extensions guide, commands in tooling/release guides,
and complete programs with their fixtures under `examples/`. READMEs summarize
and link to these pages. Recipes explain a specific task and its limits.

When changing behavior or moving code:

1. Check the relevant implementation, exported comments and behavior tests.
2. Update the owning guide, both root READMEs when their summaries change,
   and the affected recipe/example. Mark additions unavailable in a released tag.
3. Use exact API names. Label fragments and application placeholders; handle
   construction errors and show shutdown when a fragment creates a manager.
4. Run `make example` and the affected package tests. Check local paths and
   anchors; each cookbook page must be reachable from its index.

Avoid copied module counts, source-file inventories, benchmark results or
claims that every extension is built in. Discover modules through
`bash tools/modules.sh matrix`; keep dated measurements with their environment.
Local `archive/`, `plans/` and review directories are ignored by Git and are
not published API contracts. This index does not depend on those local records.
