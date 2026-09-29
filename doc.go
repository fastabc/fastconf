// Package fastconf provides a strongly typed, lock-free, Kustomize-style
// configuration loader built for Go 1.24+.
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
//     through Manager.History when WithHistory was enabled.
//
// The package examples mirror that path: ExampleNew,
// ExampleSubscribe, ExampleManager_Errors, ExampleManager_Plan, and
// ExampleHistory_Rollback.
//
// # Core ideas
//
//   - Manager[T] takes the business config struct T as a type parameter; the
//     hot read path returns *T with no reflection or allocations.
//   - State[T] is published through atomic.Pointer: one serialized writer,
//     many lock-free readers.
//   - A reload first assembles file, generator, and provider layers, then runs
//     the canonical stages Merge → Transform → Secret → TypedHooks → Decode
//     → FieldMeta → Validate → Policy before atomically publishing. Any
//     failure preserves the previous *State[T] and wraps one of eight
//     sentinels (ErrNoSources, ErrDecode, ErrMerge, ErrTransform,
//     ErrProvider, ErrInvalid, ErrClosed, ErrTooLarge).
//   - Every map or text view of a State (Map, Dump, Diff, Explain) masks
//     secrets; State.Unredacted is the explicit plaintext path.
//
// # Reading by need
//
//   - Constructors: New, Load.
//   - Loading and overlays: Option, WithProvider, WithProfile, WithWatch,
//     WithAxes, WithDir, WithFS.
//   - Runtime reaction: Subscribe, WithEqual, Manager.Errors,
//     Manager.Pause, WithObserver.
//   - Inspection and recovery: Manager.Snapshot, State.Map,
//     State.Explain, State.Dump, Manager.Plan, Manager.History.
//   - Extension points: WithTransform, WithTypedHook, WithSecretResolver,
//     WithValidate, WithPolicy, Observer, Tracer.
//
// # Module layout
//
// The main API package lives at the repository root
// (github.com/fastabc/fastconf). Independent modules with their own go.mod
// files are:
//
//	cue (unified: cue/cuelang + cue/policy)
//	integrations/cli/pflag
//	integrations/log/phuslu, integrations/log/zerolog
//	observability/metrics/prometheus, observability/otel
//	policy/opa
//	providers/s3
//	validate/playground
//
// Subpackages that share the root module version include: contracts, codec,
// confmap, transform, feature, observe, policy, integrations/render, integrations/log/internal,
// providers/{env,dotenv,cliflag,labels,source,k8s,consul,http,vault},
// cmd/fastconfctl, cmd/fastconfd and cmd/internal/cli.
// providers/s3/s3events belongs to the providers/s3 module.
// cmd/fastconfgen is its own module.
package fastconf
