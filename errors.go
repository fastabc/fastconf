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
	// ErrDecode: a file under WithDir could not be read, accepted or parsed,
	// a provider payload failed to parse, or an unknown
	// key was rejected by WithUnknownFields(UnknownError).
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
	// ErrTooLarge: a source body exceeded its size limit.
	ErrTooLarge = fcerr.ErrTooLarge
)

// ReloadError is one entry on Manager.Errors: the failure, the reload reason and its time.
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
