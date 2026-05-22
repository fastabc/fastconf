// Package fastconf provides a strongly typed, lock-free, Kustomize-style
// configuration loader built for Go 1.22+.
//
// # Start here
//
// A typical application reads FastConf in this order:
//
//   - Build a Manager[T] with New (or MustNew for one-line main / init).
//   - Read the live typed snapshot with Manager.Get.
//   - React to successful commits with Subscribe and failed reloads with
//     Manager.Errors.
//   - Preview a future commit with Manager.Plan before calling Manager.Reload.
//   - Inspect provenance through Manager.Snapshot and recover retained states
//     through Manager.Replay when WithHistory was enabled.
//
// The package examples mirror that path: ExampleNew, ExampleMustNew,
// ExampleSubscribe, ExampleManager_Errors, ExampleManager_Plan, and
// ExampleReplay_Rollback.
//
// # Core ideas
//
//   - Manager[T] takes the business config struct T as a type parameter; the
//     hot read path returns *T with no reflection or allocations.
//   - State[T] is published through atomic.Pointer: one serialized writer,
//     many lock-free readers.
//   - A reload first assembles file, generator, and provider layers, then runs
//     the canonical stages Merge → Migration → Transform → Secret →
//     TypedHooks → Decode → FieldMeta → Validate → Policy before atomically
//     publishing. Any failure preserves the previous *State[T].
//
// # Reading by need
//
//   - Constructors: New, MustNew.
//   - Preset bundles: PresetK8s, PresetSidecar, PresetTesting,
//     PresetHierarchical.
//   - Loading and overlays: Option, WithProvider, WithProfile, WithWatch,
//     WithCoalesce, WithMultiAxisOverlays, WithDir, WithFS.
//   - Runtime reaction: Subscribe, WithEqual, Manager.Errors,
//     Manager.Watcher, DiffReporter.
//   - Inspection and recovery: Manager.Snapshot, State.Introspect,
//     State.Explain, State.Dump, Manager.Plan, Manager.Replay.
//   - Extension points: Transformer, WithTypedHook, WithSecretResolver,
//     WithValidator, WithPolicy, AuditSink, MetricsSink, Tracer.
//
// # Module layout
//
// The main API package lives at the repository root
// (github.com/fastabc/fastconf). Independent modules with their own go.mod
// files are:
//
//	cmd/fastconfctl, cmd/fastconfgen
//	cue (unified: cue/cuelang + cue/policy)
//	integrations/cli/pflag, integrations/log/phuslu, integrations/log/zerolog
//	observability/metrics/prometheus, observability/otel
//	policy/opa
//	providers/s3
//	validate/playground
//
// Subpackages that share the root module version include: contracts,
// integrations/{bus,openfeature,render}, providers/{consul,http,nats,redisstream,vault,k8s},
// providers/s3/s3events, pkg/*, policy/ (root), cmd/fastconfd, and cmd/internal/cli.
//
// # Key files
//
//	manager.go   — Manager[T] facade + New + Subscribe + Eval
//	state.go     — State[T], ReloadCause, Origins/Explain/Lookup facades
//	options.go   — WithXxx option builders
//	aliases.go   — codec, secret, field-meta, and replay public facades
//	errors.go    — public sentinel errors and ReloadError stream
//	obs.go       — metrics, tracer, audit-sink facades
//	defaults.go  — WithStructDefaults + DefaulterFunc
//	feature.go   — FeatureRule extraction + Eval[T,V]
//	presets.go   — PresetK8s, PresetSidecar, PresetTesting
//	registry.go  — RegisterProviderFactory + WithProviderByName
//	bind.go      — WithSource content-type helpers
//	doc.go       — package-level godoc
package fastconf
