package manager

import (
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

func (m *M[T]) publishReloadError(reason string, err error) {
	if err == nil {
		return
	}
	// The read lock pairs with Close's write-locked close(errsCh): a
	// publish from the caller's goroutine (Reload option errors) can
	// otherwise race the close and panic on a send to a closed channel.
	m.errsMu.RLock()
	defer m.errsMu.RUnlock()
	if m.errsClosed {
		// Manager already shut down; the caller received err synchronously.
		return
	}
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
