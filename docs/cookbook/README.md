# Cookbook

Recipes follow the current checkout; additions after v1.1.0 are marked. Start
with the [loading guides](../README.md#start-here) or [complete examples](../../examples/README.md).

Go blocks are integration fragments unless labeled as complete programs.
Files use the same functional directories as the sections below.
Supply the named config type, imports and application context; `return err`
assumes an enclosing function returning `error`. Commands run from the repository
root. Optional modules and local workspace commands are listed in the
[extensions guide](../getting-started/extensions.md).

## Sources and layering

| Recipe | Purpose |
|---|---|
| [Files, profiles and patches](sources/layers.md) | Directory layout, profile selection, axes and RFC 6902 patches |
| [Environment and dotenv](sources/env-replacer.md) | Key separators, namespaces, coercion and fallback |
| [INI](sources/ini.md) | Sections, typed conversion and mixed-format merge limits |
| [Labels](sources/labels.md) | Dotted configuration, routing DSL and raw Kubernetes metadata |
| [HTTP](sources/http.md) | Remote documents, codecs, conditional requests and polling |
| [Vault KV v2](sources/vault.md) | KV data, metadata polling and authentication choices |
| [Consul KV](sources/consul.md) | Prefix trees, document mode and blocking queries |
| [Embedded files and cloud sources](sources/cloud.md) | `fs.FS`, AWS Parameter Store, GCS and Azure Blob (after v1.1.0) |
| [NATS / Redis Streams](sources/cross-process.md) | Transport reference implementations and resume limits |

## Merge, conversion and validation

| Recipe | Purpose |
|---|---|
| [Keyed-list merging](processing/strategic-merge.md) | Merge list objects with `mergeKeys` |
| [Generators](processing/generators.md) | Implement `contracts.Generator` for encoded layers |
| [Typed hooks](processing/typed-hooks.md) | Duration and scalar conversion before struct decoding |
| [`fc` tags](processing/field-meta.md) | Defaults, constraints, explicit zero values and redaction |
| [Secret resolution](processing/secrets.md) | Implement decryption hooks; configure redaction separately |
| [Policies](processing/policy.md) | Go rules and optional OPA/CUE adapters |

## Runtime updates and diagnostics

| Recipe | Purpose |
|---|---|
| [Subscriptions](runtime/subscribe.md) | Compare selected values and signal bounded resource workers |
| [Reload failures and overrides](runtime/reload-policy.md) | Error consumption and one-shot override layers |
| [Plan](runtime/plan.md) | Inspect candidate values and validator/policy findings |
| [Snapshots and history](runtime/introspect.md) | `Map`, `Dump`, `Diff`, `Explain` and rollback |
| [Timeouts and cancellation](runtime/provider-timeouts.md) | Bound provider I/O and understand writer/shutdown limits |

## Observability

| Recipe | Purpose |
|---|---|
| [Observers](observability/observer.md) | Lifecycle events, audit output and bounded async delivery |
| [Metrics and tracing](observability/observability.md) | Prometheus, OpenTelemetry and reload triage |
| [Logging](observability/log.md) | Standard `slog`, zerolog and phuslu/log |

## Deployment

| Recipe | Purpose |
|---|---|
| [Kubernetes](deployment/k8s.md) | Projected volumes and Downward API metadata |
| [Sidecar](deployment/sidecar.md) | `fastconfd` HTTP/SSE, authentication and deployment |
| [Tenants](deployment/tenant.md) | Explicit source selection and one manager per tenant |

## Application features

| Recipe | Purpose |
|---|---|
| [Feature flags](features/features.md) | Ordered targets, percentage rollouts and fallbacks |

## Migration

[Migration to v1.1.0](migration-v1.md) documents the historical v0.20.0 → v1.1.0
API changes; it is separate from recipes for the current checkout.

The [runtime contract](../design/spec.md) owns shared lifecycle, precedence,
decoding and ownership rules. Recipes link to it instead of defining another
version of those rules.
