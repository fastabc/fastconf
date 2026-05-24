# FastConf Architecture Specification

> First-public. This document describes the intended runtime model and
> compatibility boundaries for the v0.x line.

FastConf is a strongly-typed, lock-free dynamic configuration loading
and overlay framework for Go 1.22+ cloud-native applications. Design
tenets:

- **Convention over configuration** — sensible defaults at every layer.
- **Failure-safe over fail-fast** — never replace a good state with a
  broken one.
- **Strong types everywhere** — no `any` on the public read path; the
  only `any` on the public surface is `SecretRedactor.Mask(path, value)`,
  which the runtime cannot type.
- **Single writer, lock-free readers** — one reload goroutine; `Get()` is
  one `atomic.Pointer.Load`.

## 1. Core abstractions

| Interface          | Responsibility |
|--------------------|----------------|
| `Provider`         | Pull bytes / maps from a source (File, Env, CLI, KV, NATS, Redis Streams, Vault, …). |
| `Codec`            | Decode bytes into the canonical `map[string]any` intermediate. |
| `Merger`           | Kustomize-style deep merge + RFC 6902 patch + strategic merge by keyed list. |
| `Watcher`          | Subscribe to source changes with debounce + parent-directory fsnotify (K8s symlink compatible). |
| `Generator`        | Synthesise additional layers on every reload (build-info, downward-api). |
| `SecretResolver`   | Decrypt opaque ciphertext leaves before decode. |
| `TypedHook`        | Pre-decode rewrite of leaves whose type `encoding/json` cannot natively absorb (`time.Duration`, custom scalars). |
| `Transformer`      | Mutate the merged `map[string]any` before decode (defaults, env-subst, aliases, label expansion). |
| `Validator[T]`     | Cross-field invariants on the decoded `*T`. |
| `Policy[T]`        | Severity-typed declarative rules (OPA / CUE / Go). |
| `AuditSink`        | Per-successful-commit hook; receives `ReloadCause`. |
| `MetricsSink`      | Reload counters and stage timings; optional `Provider / Stage / Render` extension interfaces. |
| `Tracer` / `Span`  | OpenTelemetry-compatible span surface (root + per-stage). |
| `DiffReporter`     | Async post-commit fan-out for state diffs. |

## 2. Concurrency & memory model

- `Manager[T]` binds the business struct at construction time.
- State lives behind `atomic.Pointer[State[T]]`; `Get()` is one load —
  O(1), zero allocation, lock-free.
- Writes are serialised through a single reload goroutine fed by a
  buffered `reloadCh`.
- **Failure-safe contract**: any pipeline error preserves the previous
  `*State[T]`. Failures stream on the `m.Errors() <-chan ReloadError`
  channel (cap 16, drop-on-full); consumers are free to escalate.

## 3. Discovery & overlay rules

| Layer                                                 | Priority           | Notes |
|-------------------------------------------------------|-------------------:|-------|
| `base/*`                                              | 1000–1999          | Lexicographic; lowest precedence |
| `overlays/<profile>/*`                                | 2000–2999          | Profile expression DSL (`a & !b`) supported |
| Multi-axis overlays (`regions/<r>`, `zones/<z>`, …)   | 3000+              | Configured via `WithMultiAxisOverlays` |
| `_meta.yaml` `mergeKeys`                              | n/a                | Drives strategic merge for list-of-object slices |
| `generators`                                          | ~7000              | Between file discovery and providers |
| Providers (KV, env, CLI, Vault, Consul, NATS, …)      | per provider       | Explicit `Priority()` integer; `pkg/provider.NewBytes` defaults to 9000 |
| `WithSourceOverride` (one-shot)                       | `PriorityCLI+1000` | Single-reload transient layer |

File extensions decide kind: `.yaml/.json/.toml` → `MergeLayer`;
`.patch.yaml/.patch.json` → `PatchLayer` (RFC 6902).

## 4. Reload pipeline

```text
assemble  →  merge  →  migration  →  transform  →  secret  →
typed-hooks  →  decode  →  field-meta  →  validate  →  policy  →  commit
```

| Stage         | Description |
|---------------|-------------|
| `assemble`    | Discover layers, invoke generators, load providers. |
| `merge`       | Kustomize-style deep merge with strategic merge support. |
| `migration`   | Run registered schema migrations in version order. |
| `transform`   | Apply user `Transformer`s on the merged map. |
| `secret`      | Walk leaves and call `SecretResolver.Resolve` on every recognised ciphertext. |
| `typed-hooks` | Convert `time.Duration` etc. into JSON-friendly wire form. |
| `decode`      | `json.Marshal(merged) → json.Unmarshal(*T)`; YAML bridge optional. |
| `field-meta`  | Enforce `fastconf:"required,min,max,oneof,desc"` tags. |
| `validate`    | User `Validator[T]`s (struct-level cross-field checks). |
| `policy`      | OPA / CUE / Go policies; severity-error aborts. |
| `commit`      | Hash dedupe (cache), atomic swap, audit fan-out, subscriber fan-out, diff reporters. |

Any stage failure preserves the previous `*State[T]` and publishes one
`ReloadError` onto `m.Errors()`.

## 5. K8s-native hot reload

- **Parent-directory watch** — fsnotify watches the parent of each
  mounted file so K8s ConfigMap symlink rotation triggers correctly.
- **Debounce** — 500 ms default window absorbs Git pull / mount churn.
- **Shadow load + preflight** — every reload runs through the full
  pipeline; failure preserves the old state.
- **Hash dedupe** — global SHA-256 over `*T` drives the
  `commit()` cache so identical reloads short-circuit re-marshalling.

## 6. Cross-cutting facets

| Facet              | Surface |
|--------------------|---------|
| Provenance         | `State.Origins / Explain / Lookup`, `WithProvenance(level)` |
| History + replay   | `Manager.Replay().List / Rollback(*State[T])`, `WithHistory(n)` |
| Watch control      | `Manager.Watcher().Pause / Resume / Paused` |
| Subscriptions      | `fastconf.Subscribe[T,M](m, extract, fn)` |
| Audit              | `AuditSink`, `JSONAuditSink`, `WithAuditSink` |
| Metrics            | `MetricsSink` + `Provider / Stage / Render` extension interfaces |
| Tracing            | `Tracer` interface + `observability/otel` sub-module |
| Feature flags      | `pkg/feature.Rule`, `WithFeatureRules[T]`, `fastconf.Eval[T,V]` |
| Diff reporter      | `DiffReporter`, async post-commit fan-out |
| Tenant isolation   | `TenantManager[T]` (independent of `Manager[T]`) |
| Plan dry-run       | `Manager.Plan().WithHostname(s).Run(ctx) (*PlanResult[T], error)` |
| Sidecar            | `cmd/fastconfd` HTTP+SSE: `/healthz / /version / /config / /dump / /reload / /events` |
| Introspection      | `state.Introspect().Keys / Settings / At(path)` |
| Typed subtree      | `fastconf.Extract[T,M](state, extract) *M` |
| Marshal back       | `State.Dump(DumpYAML \| DumpJSON \| DumpTOML, redactor)` |

## 7. Module topology

| Location                                | Independent `go.mod` |
|-----------------------------------------|:--:|
| `.` (root package)                      | ✅ |
| `contracts/`                            | inside root |
| `pkg/*` (`decoder`, `discovery`, `flog`, `feature`, `generator`, `mappath`, `merger`, `migration`, `parser`, `profile`, `provider`, `source`, `transform`, `typed`, `validate`) | inside root |
| `internal/*` (`coalesce`, `diffreport`, `fcerr`, `fctypes`, `manager`, `obs`, `options`, `pipeline`, `provenance`, `registry`, `secret`, `state`, `tenant`, `testutil`, `typeinfo`, `watcher`) | inside root |
| `integrations/bus`, `integrations/openfeature`, `integrations/render` | inside root |
| `integrations/log/{phuslu,zerolog}`     | ✅ (each) |
| `providers/{vault,consul,http}`         | inside root (build-tag gated) |
| `providers/{nats,redisstream}`          | inside root (caller injects transport clients) |
| `observability/otel`                    | ✅ |
| `observability/metrics/prometheus`      | ✅ |
| `cue` (unified: cue/cuelang + cue/policy) | ✅ |
| `policy/opa`                            | ✅ |
| `validate/playground`                   | ✅ |
| `cmd/{fastconfd,fastconfctl,fastconfgen}` | inside root |

The root closure stays minimal: `yaml.v3 + json-patch + fsnotify`.
Heavy deps (OPA/CUE/OTel/Prometheus/AWS SDK/logger adapters) live in
sub-modules so the root `go get` does not pull them.

## 8. Performance contract

The hot read path is sacred:

```text
BenchmarkGet          ~0.4 ns/op   0 B/op   0 allocs/op
BenchmarkGetParallel  ~0.3 ns/op   0 B/op   0 allocs/op
```

`tools/bench-guard.sh` enforces both metrics in CI. Any change that
violates them must call out the regression in the PR description and
propose a fix or an explicit rationale. See
[`perf.md`](perf.md) for the cold-path profile.
