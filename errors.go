package fastconf

import (
	"fmt"
	"strings"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/policy"
)

// ErrFastConf is the umbrella sentinel for every error returned by
// the FastConf framework.
var ErrFastConf = fcerr.ErrFastConf

var (
	// ErrNoSources: no file, provider or generator produced a layer.
	ErrNoSources = fcerr.ErrNoSources
	// ErrDecode: a file under WithDir or its metadata could not be read,
	// accepted or parsed, a LoadFile extension or document was invalid,
	// a generator layer failed to parse, a WithOverride
	// map could not be encoded, the merged tree could not be decoded into T,
	// or an unknown key was rejected by WithUnknownFields(UnknownError).
	// Provider Load failures, including payloads a provider cannot parse,
	// wrap ErrProvider instead.
	ErrDecode = fcerr.ErrDecode
	// ErrMerge: a strict merge type conflict or a failing RFC 6902 patch layer.
	ErrMerge = fcerr.ErrMerge
	// ErrTransform: a transform, typed hook or secret resolver failed.
	ErrTransform = fcerr.ErrTransform
	// ErrProvider: a provider or generator Load failed.
	ErrProvider = fcerr.ErrProvider
	// ErrInvalid: a validator, field-meta rule or policy rejected the
	// decoded value; PolicyError satisfies it too.
	ErrInvalid = fcerr.ErrInvalid
	// ErrClosed: the manager was closed.
	ErrClosed = fcerr.ErrClosed
	// ErrPaused: a notifier refresh was skipped because the manager is paused.
	// The consumer must not acknowledge the event as successfully refreshed.
	ErrPaused = fcerr.New("fastconf: manager paused")
	// ErrTooLarge: a source body exceeded its size limit.
	ErrTooLarge = fcerr.ErrTooLarge
	// ErrPanic: a transform, secret resolver, typed hook, Defaults method,
	// validator, policy, provider, generator or other user code running on
	// the reload pipeline or initial default-tag validation panicked, or a
	// codec panicked while LoadFile decoded its file. The error names the
	// stage and the panic value; the stack is logged at Error level.
	ErrPanic = fcerr.ErrPanic
)

// ReloadError is one entry on Manager.Errors: the failure, the free-form reload
// reason, its bounded Trigger (see ReloadTrigger) and its time.
type ReloadError = fcerr.ReloadError

// PolicyError aggregates the violations that aborted a reload.
type PolicyError struct {
	Violations []policy.Violation
}

// Error describes the policy violations that aborted the reload.
func (e *PolicyError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s@%s: %s", v.Rule, v.Path, v.Message))
	}
	return "fastconf: policy denied: " + strings.Join(parts, "; ")
}

// Is reports whether target is ErrInvalid or ErrFastConf.
func (e *PolicyError) Is(target error) bool {
	return target == ErrInvalid || target == ErrFastConf
}
