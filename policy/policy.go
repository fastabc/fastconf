// Package policy defines typed policies evaluated after validation and before publication. Errors
// preserve the old snapshot. Heavy backends live in github.com/fastabc/fastconf/policy/opa and github.com/fastabc/fastconf/cue/policy.
package policy

import "context"

// Severity classifies a Violation. Manager treats Error as a hard reload failure; Warning is
// recorded (Plan reports it) but does not block the swap.
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

// Input is the typed evaluation context passed to Policy.Evaluate. Fields are read-only — policies
// MUST NOT mutate Config.
type Input[T any] struct {
	// Config is the freshly decoded, validated, but NOT-yet-published
	// configuration. The pointer is stable for the duration of the
	// Evaluate call.
	Config *T
	// Reason mirrors ReloadCause.Reason ("provider:vault", "watcher", ...).
	Reason string
	// Tenant carries the WithTenant id, if any.
	Tenant string
}

// Policy is the contract every policy backend implements. Evaluate MUST be goroutine-safe and
// SHOULD return promptly; the manager invokes it inline on the reload goroutine.
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
