package fastconf

import (
	"fmt"
	"strings"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/policy"
)

var ErrFastConf = fcerr.ErrFastConf

var (
	ErrNoSources = fcerr.ErrNoSources
	ErrDecode    = fcerr.ErrDecode
	ErrMerge     = fcerr.ErrMerge
	ErrTransform = fcerr.ErrTransform
	ErrProvider  = fcerr.ErrProvider
	ErrInvalid   = fcerr.ErrInvalid
	ErrClosed    = fcerr.ErrClosed
	ErrTooLarge  = fcerr.ErrTooLarge
)

type ReloadError = fcerr.ReloadError

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
	return target == ErrInvalid || target == ErrFastConf
}

// closedErrCh is returned by Errors() on a nil manager, avoiding a per-call
// allocation.
var closedErrCh = func() chan ReloadError {
	ch := make(chan ReloadError)
	close(ch)
	return ch
}()

// Errors returns a buffered channel that publishes one ReloadError per
// failed reload attempt. The channel has a fixed capacity; if the
// consumer cannot keep up, the oldest pending error is dropped so the
// reload loop never blocks. Closed by Close.
//
// Note: the synchronous error returned by Reload(ctx, ...) (and Plan()
// failures) is also published here, so a consumer can centralise error
// handling without checking both paths.
func (m *Manager[T]) Errors() <-chan ReloadError {
	if m == nil {
		return closedErrCh
	}
	return m.errsCh
}

func (m *Manager[T]) publishReloadError(reason string, err error) {
	if err == nil {
		return
	}
	// Only the reload goroutine (reloadLoop, and subscriber callbacks it
	// runs) publishes, so no send can race Close's close(errsCh).
	re := fcerr.ReloadError{Err: err, Reason: reason, When: time.Now()}
	for {
		select {
		case m.errsCh <- re:
			return
		default:
		}
		select {
		case <-m.errsCh:
		default:
			return
		}
	}
}
