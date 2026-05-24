# FastConf Cookbook — Recipes by Journey

Each recipe is self-contained. Start with the path that matches the work in
front of you, then follow the related recipes when the deployment gets deeper.

## Day 1 — get a typed config loading

| Recipe | Use it for |
|---|---|
| [k8s](k8s.md) | ConfigMap / Secret + sidecar / in-process loading |
| [sidecar](sidecar.md) | Run `fastconfd` for non-Go workloads |
| [env-replacer](env-replacer.md) | Env key conventions, `At()` namespacing, coercion |
| [labels](labels.md) | Metadata labels, dotted config labels, routing DSL labels |
| [migration v0.18](migration-v0.18.md) | Move application code onto the v0.18 layout |
| [migration v0.19](migration-v0.19.md) | Update `Subscribe` callbacks for diff-aware defaults |

## Operations — observe, inspect, and run safely

| Recipe | Use it for |
|---|---|
| [otel](otel.md) | Wire OpenTelemetry tracing for reload spans |
| [log](log.md) | JSON / zerolog / phuslu/log adapter wiring |
| [diff-reporter](diff-reporter.md) | Push reload diffs to Slack / PagerDuty / GitHub |
| [dump](dump.md) | Marshal current state to deterministic YAML |
| [introspect](introspect.md) | `state.Introspect().Keys / Settings / At` |
| [reload-policy](reload-policy.md) | `m.Errors()` consumer pattern + `WithSourceOverride` |
| [provider-timeouts](provider-timeouts.md) | HTTP-client `Timeout` vs `ctx` guarantees |
| [vault](vault.md) | Pull from Vault KV-v2, leases, and rotation |
| [consul](consul.md) | Consul KV with watcher prefix + ACL |
| [cross-process](cross-process.md) | Cross-process push via NATS or Redis Streams |

## Advanced — policy, generators, and extension points

| Recipe | Use it for |
|---|---|
| [plan](plan.md) | Dry-run a reload before letting it land |
| [policy](policy.md) | OPA / CUE / Go policies in the reload pipeline |
| [tenant](tenant.md) | Multi-tenant config in one process |
| [generators](generators.md) | Kustomize-style ConfigMap/Secret generators |
| [strategic-merge](strategic-merge.md) | `mergeKeys` list-of-object merge |
| [typed-hooks](typed-hooks.md) | Plug `time.Duration` / custom scalar pre-decode hooks |
| [field-meta](field-meta.md) | `fastconf:"required,min=,max=,oneof=,desc="` metadata |
| [secrets](secrets.md) | SOPS / age / KMS / Vault transit decryption hooks |
| [features](features.md) | Feature flags, targeting, percentage rollouts |
| [openfeature](openfeature.md) | Adapt FastConf as an OpenFeature provider |
