package fcerr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fastabc/fastconf/policy"
)

// ErrFastConf is the umbrella sentinel for every error returned by
// the FastConf framework.
var ErrFastConf = errors.New("fastconf")

type fcErr struct{ s string }

func (e *fcErr) Error() string        { return e.s }
func (e *fcErr) Is(target error) bool { return target == ErrFastConf }

func newFCErr(msg string) *fcErr { return &fcErr{s: msg} }

var (
	ErrNoSources  = newFCErr("fastconf: no configuration sources discovered")
	ErrValidation = newFCErr("fastconf: validation failed")
	ErrDecode     = newFCErr("fastconf: decode failed")
	ErrMerge      = newFCErr("fastconf: merge failed")
	ErrPatch      = newFCErr("fastconf: patch failed")
	ErrClosed     = newFCErr("fastconf: manager closed")
	ErrValidator  = newFCErr("fastconf: validator failed")
	ErrTransform  = newFCErr("fastconf: transform failed")
	ErrNoOrigin   = newFCErr("fastconf: no origin for path")
)

// ReloadError is one entry on the Manager.Errors() channel.
type ReloadError struct {
	Err    error
	Reason string
	When   time.Time
}

const ErrorChanCap = 16

// ErrPolicyDenied is returned when one or more SeverityError policy
// violations abort a reload.
var ErrPolicyDenied = errors.New("fastconf: policy denied")

// PolicyError aggregates the violations that aborted a reload.
type PolicyError struct {
	Violations []policy.Violation
}

func (e *PolicyError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s@%s: %s", v.Rule, v.Path, v.Message))
	}
	return "fastconf: policy denied: " + strings.Join(parts, "; ")
}

func (e *PolicyError) Is(target error) bool {
	return target == ErrPolicyDenied || target == ErrFastConf
}
