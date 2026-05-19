// Package policy defines the policy interface. Policies run
// inside the reload pipeline AFTER decode and validation but BEFORE
// the atomic state swap, so a violation cleanly aborts the reload
// without breaking the failure-safe contract: the previous *State[T]
// remains in place and Get() callers see no glitch.
//
// The interface is generic in T so a policy can inspect the strongly
// typed configuration directly — no map[string]any round-trip.
//
// Heavy policy backends (OPA, CUE) live in the corresponding
// fastconf/policy/opa and fastconf/policy/cue submodules to keep the
// core dependency-free.
package policy

import "context"

// Severity classifies a Violation. Manager treats Error as a hard
// reload failure; Warning is logged and forwarded to AuditSink but
// does not block the swap.
type Severity int

const (
	SeverityWarning Severity = iota
	SeverityError
)

// Violation is a single policy finding.
type Violation struct {
	Path     string
	Message  string
	Severity Severity
	// Rule is the policy rule id that produced this violation; empty
	// for ad-hoc closures.
	Rule string
}

// Input is the typed evaluation context passed to Policy.Evaluate.
// Fields are read-only — policies MUST NOT mutate Config.
type Input[T any] struct {
	// Config is the freshly decoded, validated, but NOT-yet-published
	// configuration. The pointer is stable for the duration of the
	// Evaluate call.
	Config *T
	// Reason mirrors ReloadCause.Reason ("provider:vault", "watcher", ...).
	Reason string
	// Tenant carries the TenantManager id when applicable.
	Tenant string
}

// Policy is the contract every policy backend implements. Evaluate
// MUST be goroutine-safe and SHOULD return promptly; the manager
// invokes it inline on the reload goroutine.
type Policy[T any] interface {
	Name() string
	Evaluate(ctx context.Context, in Input[T]) ([]Violation, error)
}

// Func adapts a free function into a Policy.
type Func[T any] struct {
	N  string
	Fn func(context.Context, Input[T]) ([]Violation, error)
}

func (f Func[T]) Name() string { return f.N }
func (f Func[T]) Evaluate(ctx context.Context, in Input[T]) ([]Violation, error) {
	return f.Fn(ctx, in)
}

// AnyPolicy is the type-erased shim used by the framework to keep
// the non-generic options.policies slice. End users never construct
// AnyPolicy directly — they call fastconf.WithPolicy(p) which uses
// Adapt under the hood.
type AnyPolicy interface {
	Name() string
	EvaluateAny(ctx context.Context, cfg any, reason, tenant string) ([]Violation, error)
}

// Adapt converts a typed Policy[T] into the framework-internal
// AnyPolicy representation. The conversion fails fast at evaluation
// time if the runtime type does not match T (defensive — the
// framework always passes *T, so the assertion is a safety net).
func Adapt[T any](p Policy[T]) AnyPolicy {
	return adapter[T]{p: p}
}

type adapter[T any] struct{ p Policy[T] }

func (a adapter[T]) Name() string { return a.p.Name() }
func (a adapter[T]) EvaluateAny(ctx context.Context, cfg any, reason, tenant string) ([]Violation, error) {
	c, ok := cfg.(*T)
	if !ok {
		return []Violation{{
			Path:     "",
			Message:  "policy: type mismatch (framework bug)",
			Severity: SeverityError,
			Rule:     a.p.Name(),
		}}, nil
	}
	return a.p.Evaluate(ctx, Input[T]{Config: c, Reason: reason, Tenant: tenant})
}
