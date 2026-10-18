# Documentation

These pages describe the current FastConf API. Commands run from the repository
root unless a recipe says otherwise.

| Need | Start here |
|---|---|
| Load one file at startup | [Single-file guide](getting-started/load.md) |
| Manage runtime changes | [Dynamic guide](getting-started/dynamic.md) |
| Add providers or policies | [Extensions guide](getting-started/extensions.md) |
| Install and load configuration | [English README](../README.md), [中文 README](../README.zh.md) |
| Run complete programs | [Examples](../examples/README.md), including S3 and pflag examples |
| Find a recipe by feature | [Cookbook](cookbook/README.md) |
| Understand lifecycle, precedence and ownership | [Runtime contract](design/spec.md) |
| Locate implementation and tests | [Architecture](architecture.md) |
| Measure performance and resource recovery | [Performance notes](design/perf.md) |
| Run development checks | [Repository tooling](../tools/README.md) |
| Prepare a release | [Release guide](../RELEASING.md) |
| Upgrade from v0.20.0 | [Migration guide](cookbook/migration-v1.md) |

## Maintaining documentation

Keep behavior definitions in the runtime contract, commands in the tooling and
release guides, and task-specific examples in the cookbook. Link to those
sources instead of repeating full option tables or module inventories.

When behavior or paths change, check exported Go comments, both READMEs,
relevant recipes, examples and CI/Makefile commands together. Run `make example` to validate core and optional
examples; verify local links and anchors after moves.

`archive/` contains dated validation and review records. Their results describe
the recorded checkout and environment, not the current release. Local drafts in
`plans/` are ignored by Git and are not published documentation or API contracts.
