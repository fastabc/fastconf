package manager

import (
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

func (m *M[T]) publishReloadError(reason string, err error) {
	if err == nil {
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
