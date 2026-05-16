// Package fastconf provides a strongly typed, lock-free, Kustomize-style
// configuration loader built on Go 1.26 generics.
//
// # Start here
//
// A typical application reads FastConf in this order:
//
//   - Build a Manager[T] with New.
//   - Read the live typed snapshot with Manager.Get.
//   - React to successful commits with Subscribe and failed reloads with
//     Manager.Errors.
//   - Preview a future commit with Manager.Plan before calling Manager.Reload.
//   - Inspect provenance through Manager.Snapshot and recover retained states
//     through Manager.Replay when WithHistory was enabled.
//
// The package examples mirror that path: ExampleNew, ExampleSubscribe,
// ExampleManager_Errors, ExampleManager_Plan, and ExampleReplay_Rollback.
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
//   - Loading and overlays: New, Option, PresetK8s, PresetSidecar,
//     WithProvider, WithProfile, WithMultiAxisOverlays.
//   - Runtime reaction: Subscribe, Manager.Errors, Manager.Watcher,
//     DiffReporter.
//   - Inspection and recovery: Manager.Snapshot, State.Introspect,
//     State.Explain, Manager.Plan, Manager.Replay.
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
//	integrations/log/phuslu, integrations/log/zerolog, integrations/openfeature
//	observability/metrics/prometheus, observability/otel
//	policy/cue, policy/opa
//	providers/nats, providers/redisstream
//	validate/cue/cuelang, validate/playground
//
// Subpackages that share the root module version include: contracts,
// integrations/{bus,render}, providers/{consul,http,vault}, pkg/*, policy/
// (root), cmd/fastconfd, and cmd/internal/cli.
//
// # Key files
//
//	manager.go            — Manager[T] lifecycle + serialized reload loop
//	provider_watch.go     — provider event subscription + resume fallback
//	pipeline.go           — assemble / commit / Plan / codec registry
//	pipeline_stages.go    — canonical merge→policy stage definitions
//	state.go              — State[T], provenance, history, diff, watcher views
//	options.go            — WithXxx option builders
//	feature.go            — feature-rule extraction + Eval[T,V]
//	introspect.go         — dotted-key diagnostics + Sub[T,M]
//	obs_audit.go          — audit sinks and JSON audit output
//	obs_metrics.go        — metrics extension points and bridge
//	obs_tracer.go         — tracing extension points and noop tracer
//	errors.go             — public sentinel errors and reload error stream
//	watch.go / watcher.go — subscriptions + file-system watcher runtime
package fastconf
