package fastconf

import (
	"errors"
	"time"
)

// ErrFastConf is the umbrella sentinel for every error returned by
// the FastConf framework. Every public Err* below chains to it via an
// Is method so callers can write a single catch-all clause:
//
//if errors.Is(err, fastconf.ErrFastConf) { ... }
//
// Centralised error hierarchy. Each sentinel is
// implemented as *fcErr; errors.Is matches both the specific sentinel
// (by pointer identity) and ErrFastConf (via fcErr.Is).
var ErrFastConf = errors.New("fastconf")

// fcErr is the concrete type behind every Err* sentinel below. It
// carries a fixed message and reports membership in the FastConf
// error family through Is(target) so wrappers built with fmt.Errorf
// "%w" automatically participate in the hierarchy.
type fcErr struct{ s string }

func (e *fcErr) Error() string { return e.s }

// Is returns true for the umbrella ErrFastConf in addition to the
// per-instance pointer equality that errors.Is checks first.
func (e *fcErr) Is(target error) bool { return target == ErrFastConf }

func newFCErr(msg string) *fcErr { return &fcErr{s: msg} }

// Package-level sentinels. Every error returned from the public API
// satisfies errors.Is against one of these AND against ErrFastConf.
var (
// ErrNoSources is returned when discovery + providers produced no layers.
ErrNoSources = newFCErr("fastconf: no configuration sources discovered")
// ErrValidation indicates *T failed structural validation.
ErrValidation = newFCErr("fastconf: validation failed")
// ErrDecode indicates a layer could not be decoded.
ErrDecode = newFCErr("fastconf: decode failed")
// ErrMerge indicates the deep-merge stage rejected an inconsistency.
ErrMerge = newFCErr("fastconf: merge failed")
// ErrPatch indicates an RFC 6902 patch failed to apply.
ErrPatch = newFCErr("fastconf: patch failed")
// ErrClosed indicates the Manager has been closed.
ErrClosed = newFCErr("fastconf: manager closed")
// ErrValidator indicates a WithValidator callback returned an error.
ErrValidator = newFCErr("fastconf: validator failed")
// ErrTransform indicates a WithTransformers callback returned an error.
ErrTransform = newFCErr("fastconf: transform failed")
// ErrNoOrigin indicates LookupStrict found no provenance for path.
ErrNoOrigin = newFCErr("fastconf: no origin for path")
)


// ---------------------------------------------------------------------
// Reload error stream.
// ---------------------------------------------------------------------

// ReloadError is one entry on the Manager.Errors() channel. Failure-safe
// is unchanged: on every reload failure the previous *State[T] remains
// active; this struct is purely a notification carrier.
type ReloadError struct {
	// Err is the wrapped reload error (errors.Is(err, ErrFastConf) → true).
	Err error
	// Reason mirrors the reloadRequest reason ("manual" / "watcher" /
	// "provider:vault" / "override" / ...). Safe for log labels and
	// metric dimensions.
	Reason string
	// When is the wall-clock instant the reload attempt completed.
	When time.Time
}

// errorChanCap caps the Errors() ring. When the consumer cannot drain in
// time, the oldest pending error is dropped; failure-safe state-keeping
// is unaffected.
const errorChanCap = 16

// publishReloadError sends e onto m.errsCh, dropping the oldest pending
// error if the consumer is slow. Called by reloadLoop after each failed
// reload attempt.
func (m *Manager[T]) publishReloadError(reason string, err error) {
	if err == nil {
		return
	}
	re := ReloadError{Err: err, Reason: reason, When: time.Now()}
	for {
		select {
		case m.errsCh <- re:
			return
		default:
		}
		// Drop the oldest pending error to make room.
		select {
		case <-m.errsCh:
		default:
			return
		}
	}
}
