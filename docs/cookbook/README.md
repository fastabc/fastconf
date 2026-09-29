# FastConf Cookbook — Recipes by Journey

Recipes target the FastConf v1.0.0 API. Unless marked as a
complete program, Go blocks are integration fragments: supply your own config
type, imports, context, error handling and manager shutdown.

Start with the [runnable examples](../../examples/README.md) for complete source,
configuration, commands and expected output. Root-only examples require Go 1.24+;
optional integrations use their module's declared Go floor. Commands run from the
repository root unless stated otherwise. To test optional modules against local
source, use `bash tools/modules.sh exec <command>`.
Historical API names appear only in migration tables and before/after blocks.

The [runtime contract](../design/spec.md) defines lifecycle, source precedence,
decoding, metadata file layout and configuration ownership for all recipes.

## Day 1 — get a typed config loading

| Recipe | Use it for |
|---|---|
| [Local layered configuration](../../examples/basic/README.md) | Complete base/prod/env example with typed reads and validation |
| [INI input](ini.md) | Sections, string values, typed conversion and strict mixed-format merging |
| [k8s](k8s.md) | ConfigMap / Secret + sidecar / in-process loading |
| [sidecar](sidecar.md) | Run `fastconfd` for non-Go workloads |
| [env-replacer](env-replacer.md) | Env key conventions, `At()` namespacing, coercion |
| [labels](labels.md) | Metadata labels, dotted config labels, routing DSL labels |
| [migration v1](migration-v1.md) | Upgrade directly from v0.20.0 to v1.0.0 |

## Operations — observe, inspect, and run safely

| Recipe | Use it for |
|---|---|
| [observability](observability.md) | Prometheus rules, OpenTelemetry tracing, and reload triage |
| [log](log.md) | JSON / zerolog / phuslu/log adapter wiring |
| [observer](observer.md) | Audit lines, metrics and pushing reload diffs to Slack / PagerDuty / GitHub |
| [introspect](introspect.md) | `state.Map / Dump / Explain`, `Unredacted()`, deterministic dumps |
| [reload-policy](reload-policy.md) | `m.Errors()` consumer pattern + `WithOverride` |
| [provider-timeouts](provider-timeouts.md) | HTTP-client `Timeout` vs `ctx` guarantees |
| [vault](vault.md) | Load Vault KV-v2 and poll metadata for version changes |
| [consul](consul.md) | Consul KV with watcher prefix + ACL |
| [cross-process](cross-process.md) | Cross-process push via NATS or Redis Streams |

## Advanced — policy, generators, and extension points

| Recipe | Use it for |
|---|---|
| [plan](plan.md) | Dry-run a reload before letting it land |
| [policy](policy.md) | OPA / CUE / Go policies in the reload pipeline |
| [tenant](tenant.md) | Complete local shared/prod/tenant files, isolated env and reload failures |
| [generators](generators.md) | Kustomize-style ConfigMap/Secret generators |
| [strategic-merge](strategic-merge.md) | `mergeKeys` list-of-object merge |
| [typed-hooks](typed-hooks.md) | Plug `time.Duration` / custom scalar pre-decode hooks |
| [field-meta](field-meta.md) | `fc:"required,min=,max=,oneof=,desc="` metadata |
| [secrets](secrets.md) | SOPS / age / KMS / Vault transit decryption hooks |
| [features](features.md) | Feature flags, targeting, percentage rollouts |
