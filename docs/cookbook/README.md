# FastConf Cookbook

This is the canonical recipe index. Each page is intentionally self-contained:
pick the one deployment shape you need instead of reconstructing a flow from the
root README.

## Core

| Recipe | When to use |
|--------|-------------|
| [k8s](k8s.md)              | ConfigMap / Secret + sidecar / in-process |
| [plan](plan.md)            | Dry-run a reload before letting it land |
| [policy](policy.md)        | OPA / CUE / Go policies in the reload pipeline |
| [tenant](tenant.md)        | Multi-tenant config in one process |
| [sidecar](sidecar.md)      | Run `fastconfd` for non-Go workloads |
| [log](log.md)              | JSON / zerolog / phuslu/log adapter wiring |

## Providers & sources

| Recipe | When to use |
|--------|-------------|
| [vault](vault.md)                  | Pull from Vault KV-v2, leases & rotation |
| [consul](consul.md)                | Consul KV with watcher prefix + ACL |
| [cross-process](cross-process.md)  | Cross-process push via NATS or Redis Streams |
| [generators](generators.md)        | Kustomize-style ConfigMap/Secret generators (build-info, downward-api) |
| [labels](labels.md)                | Traefik / Docker / K8s label expansion |
| [env-replacer](env-replacer.md)    | Viper-style env key replacer + auto-bind |
| [provider-timeouts](provider-timeouts.md) | HTTP-client `Timeout` vs `ctx` — what FastConf guarantees |

## Merge & decode

| Recipe | When to use |
|--------|-------------|
| [strategic-merge](strategic-merge.md) | `mergeKeys` — Kustomize-style list-of-object merge |
| [typed-hooks](typed-hooks.md)         | Plug `time.Duration` / custom-scalar pre-decode hooks |
| [field-meta](field-meta.md)           | `fastconf:"required,min=,max=,oneof=,desc="` struct metadata |

## Secrets & rollouts

| Recipe | When to use |
|--------|-------------|
| [secrets](secrets.md)         | SOPS / age / KMS / Vault transit decryption hook |
| [features](features.md)       | Feature flags, targeting, percentage rollouts |
| [openfeature](openfeature.md) | Adapt FastConf as an OpenFeature provider |

## Operators & introspection

| Recipe | When to use |
|--------|-------------|
| [introspect](introspect.md)         | `state.Introspect().Keys / Settings / At` |
| [dump](dump.md)                     | Marshal current state to deterministic YAML |
| [diff-reporter](diff-reporter.md)   | Push reload diffs to Slack / PagerDuty / GitHub |
| [reload-policy](reload-policy.md)   | `m.Errors()` consumer pattern + `WithSourceOverride` |

## Observability

| Recipe | When to use |
|--------|-------------|
| [otel](otel.md) | Wire OpenTelemetry tracing for reload spans |
