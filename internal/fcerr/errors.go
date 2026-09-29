package fcerr

import (
	"errors"
	"time"
)

// ErrFastConf is the umbrella sentinel for every error returned by
// the FastConf framework.
var ErrFastConf = errors.New("fastconf")

type fcErr struct{ s string }

func (e *fcErr) Error() string        { return e.s }
func (e *fcErr) Is(target error) bool { return target == ErrFastConf }

func newFCErr(msg string) *fcErr { return &fcErr{s: msg} }

// New returns a sentinel that satisfies errors.Is(err, ErrFastConf). Every
// exported FastConf sentinel is built with it.
func New(msg string) error { return newFCErr(msg) }

// The eight reload sentinels, split by what the caller does next: fix a
// file (ErrDecode), fix an overlay (ErrMerge), fix code (ErrTransform),
// retry (ErrProvider), fix a value (ErrInvalid).
var (
	// ErrNoSources: no file, provider or generator produced a layer.
	ErrNoSources = newFCErr("fastconf: no configuration sources discovered")
	// ErrDecode: a file or provider payload failed to parse, or an unknown
	// key was rejected by WithUnknownFields(UnknownError).
	ErrDecode = newFCErr("fastconf: decode failed")
	// ErrMerge: a strict merge type conflict or a failing _patch.json.
	ErrMerge = newFCErr("fastconf: merge failed")
	// ErrTransform: a transform, typed hook or secret resolver failed.
	ErrTransform = newFCErr("fastconf: transform failed")
	// ErrProvider: a provider or generator Load failed.
	ErrProvider = newFCErr("fastconf: provider failed")
	// ErrInvalid: a validator, field-meta rule or policy rejected the
	// decoded value; PolicyError satisfies it too.
	ErrInvalid = newFCErr("fastconf: invalid configuration")
	// ErrClosed: the manager was closed.
	ErrClosed = newFCErr("fastconf: manager closed")
	// ErrTooLarge: a source body exceeded its size limit.
	ErrTooLarge = newFCErr("fastconf: configuration body too large")
)

// ReloadError is one entry on the Manager.Errors() channel.
type ReloadError struct {
	Err    error
	Reason string
	When   time.Time
}

const ErrorChanCap = 16
