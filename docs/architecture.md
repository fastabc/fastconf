# FastConf Architecture

This page is the canonical directory and dependency overview for the v0.18
layout. Keep `tools/check-layout.sh`, `tools/check-deps.sh`, and this document
in sync when moving packages.

## Directory Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│                  ROOT FACADE  (package fastconf)                     │
│  12 .go files                                                        │
│  • aliases.go  bind.go   defaults.go  doc.go   errors.go             │
│  • feature.go  manager.go obs.go      options.go presets.go          │
│  • registry.go state.go                                              │
│  Public surface: type aliases / With* options / constructors         │
└──────────────────────────────────────────────────────────────────────┘
                          │  type-aliases + delegation
                          ▼
┌──────────────────────────────────────────────────────────────────────┐
│            IMPLEMENTATION  (internal/* + pkg/* + contracts/*)        │
│  internal/* : private implementation packages                         │
│     coalesce  diffreport  fcerr   fctypes   manager  obs             │
│     options   pipeline    provenance registry secret  state          │
│     tenant    testutil    typeinfo  watcher                          │
│  pkg/*      : publicly reusable primitives                            │
│     cliadapter decoder discovery feature flog generator mappath       │
│     merger migration parser profile provider source transform typed   │
│     validate                                                         │
│  contracts/ : public stable interfaces (Provider/Codec/Event/...)    │
└──────────────────────────────────────────────────────────────────────┘
                          │  read by sub-modules via require + replace
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
| `cue/` | CUE sub-module. |
| `docs/` | Design docs, plans, cookbook, and README chapters. |
| `examples/` | Runnable scenario examples outside the root facade package. |
| `integrations/` | Optional integration adapters. |
| `internal/` | Private implementation packages protected by Go's internal boundary. |
| `observability/` | Metrics and tracing sub-modules. |
| `pkg/` | Public reusable primitives. |
| `policy/` | Policy backends. |
| `providers/` | Built-in and satellite providers. |
| `tools/` | Repository guard scripts. |
| `validate/` | Validation playground sub-module. |
| `.github/` | CI workflows. |

## Dependency Direction

```
fastconf  →  internal/manager
          →  internal/options
          →  internal/state
          →  internal/tenant
          →  pkg/discovery
          →  pkg/decoder
          →  pkg/flog
          →  pkg/merger
          →  pkg/provider
          →  pkg/validate

pkg/* 之间不得相互依赖，仅以下白名单例外（与 tools/check-deps.sh 同步）：
  pkg/discovery → pkg/profile
  pkg/generator → pkg/mappath   (leaf util)
  pkg/provider  → pkg/decoder
  pkg/provider  → pkg/mappath   (leaf util)
  pkg/provider  → pkg/typed     (leaf util)
  pkg/mappath   → pkg/typed     (leaf util)
  pkg/transform → pkg/mappath   (leaf util)
  pkg/parser    → pkg/decoder   (parser wraps codec; adds ContentTypes)
internal/* 是实现层，可按需依赖同层包；对外只暴露 root facade。
pkg/flog 仅依赖标准库（log/slog/runtime/sync/time/context）。
```

CI enforces the package dependency direction through `tools/check-deps.sh`.
