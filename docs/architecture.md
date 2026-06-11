# FastConf Architecture

This page is the canonical directory and dependency overview for the current
layout. Keep `tools/check-layout.sh` and this document in sync when moving
packages.

## Directory Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│                  ROOT FACADE  (package fastconf)                     │
│  13 .go files                                                        │
│  • aliases.go  bind.go   defaults.go  doc.go   errors.go             │
│  • feature.go  manager.go obs.go      options.go presets.go          │
│  • registry.go state.go validate.go                                  │
│  Public surface: type aliases / With* options / constructors         │
└──────────────────────────────────────────────────────────────────────┘
                          │  type-aliases + delegation
                          ▼
┌──────────────────────────────────────────────────────────────────────┐
│       IMPLEMENTATION  (internal/* + public domain packages)          │
│  internal/* : private implementation packages                         │
│     coalesce  diffreport  fcerr   manager   obs      options         │
│     pipeline  providerutil provenance registry secret state          │
│     tenant    testutil    typeinfo watcher                           │
│  public domain packages:                                              │
│     codec  confmap  overlay  transform  feature  policy  providers/* │
│  contracts/ : public stable interfaces (Provider/Codec/Event/...)    │
└──────────────────────────────────────────────────────────────────────┘
                          │  read by sub-modules via require (go.work in-repo)
                          ▼
┌──────────────────────────────────────────────────────────────────────┐
│   SATELLITE MODULES                                                  │
│  cue/                  CUE validation + policy backend                │
│  providers/s3          S3 provider + s3events subpackage              │
│  observability/metrics/prometheus                                    │
│  observability/otel                                                  │
│  policy/opa                                                         │
│  validate/playground                                                 │
│  integrations/log/phuslu                                             │
│  integrations/log/zerolog                                            │
│  integrations/cli/pflag                                              │
│  Root-module providers: consul/http/nats/redisstream/vault/k8s       │
└──────────────────────────────────────────────────────────────────────┘
```

## Top-Level Directories

| Directory | Role |
|---|---|
| `cmd/` | Command binaries. |
| `contracts/` | Stable public interfaces. |
| `codec/` | Codec registry, built-in decoders, parser registry, typed hooks. |
| `confmap/` | `map[string]any` merge, path, label expansion, and scalar coercion helpers. |
| `cue/` | CUE sub-module. |
| `docs/` | Design docs, plans, cookbook, and README chapters. |
| `examples/` | Runnable scenario examples outside the root facade package. |
| `feature/` | Feature flag rule evaluation. |
| `integrations/` | Optional integration adapters. |
| `internal/` | Private implementation packages protected by Go's internal boundary. |
| `observability/` | Metrics and tracing sub-modules. |
| `overlay/` | Overlay discovery and profile expression evaluation. |
| `policy/` | Policy backends. |
| `providers/` | Built-in and satellite providers. |
| `tools/` | Repository guard scripts. |
| `transform/` | Raw-map transformers and schema migration helpers. |
| `validate/` | Validation playground sub-module. |
| `.github/` | CI workflows. |

## Dependency Direction

```
fastconf  →  internal/manager
          →  internal/options
          →  internal/state
          →  internal/tenant
          →  internal/flog
          →  codec
          →  confmap
          →  overlay
          →  transform
          →  providers/*
          →  contracts

Public domain packages form a small DAG:
  codec       → contracts
  overlay     → codec
  transform   → confmap
  providers/* → codec, confmap, contracts

internal/* 是实现层，可按需依赖同层包；对外只暴露 root facade。
internal/flog 仅依赖标准库（log/slog/runtime/sync/time/context）。
```

CI enforces the layout and dependency direction through `tools/check-layout.sh` and `go list -deps`.
