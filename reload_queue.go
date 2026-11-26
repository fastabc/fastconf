package fastconf

import (
	"context"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// reloadChanCap bounds pending work. Provider overflow retains one resync token;
// manual and coalesced file reloads wait for space or cancellation.
const reloadChanCap = 16

// reloadRequest carries caller cancellation through the writer and pipeline.
// A nil ctx from watcher triggers uses the manager lifetime; hooks without a
// context must return before the writer can finish.
// trigger is the bounded classification of reason; every non-test producer
// sets it (see ReloadTrigger).
type reloadRequest struct {
	ctx     context.Context
	trigger ReloadTrigger
	reason  string
	key     string                      // optional: parent dir for fs-driven reloads (audit dim)
	run     func(context.Context) error // if non-nil, called instead of m.reload (e.g. rollback)
	doneCh  chan error
}

// requestReload queues work with the caller's context and waits for its result,
// ErrClosed, or cancellation. Cancellation also reaches the in-flight pipeline.
func (m *Manager[T]) requestReload(ctx context.Context, trigger ReloadTrigger, reason, key string) error {
	return m.submitAndWait(ctx, reloadRequest{trigger: trigger, reason: reason, key: key})
}

func (m *Manager[T]) submitAndWait(ctx context.Context, req reloadRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-m.lifetime.Done():
		return fcerr.ErrClosed
	default:
	}
	if req.ctx == nil {
		req.ctx = ctx
	}
	if req.doneCh == nil {
		req.doneCh = make(chan error, 1)
	}
	select {
	case m.reloadCh <- req:
	case <-m.lifetime.Done():
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.lifetime.Done():
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rejectPendingReloads drains any already-enqueued reload requests and
// signals their waiters with fcerr.ErrClosed so Close never strands callers.
func (m *Manager[T]) rejectPendingReloads() {
	for {
		select {
		case req := <-m.reloadCh:
			if req.doneCh != nil {
				req.doneCh <- fcerr.ErrClosed
			}
		default:
			return
		}
	}
}

// reloadLoop is the single writer goroutine. It serializes every
// reload request so that no two reload pipelines ever interleave.
func (m *Manager[T]) reloadLoop() {
	defer m.bgWG.Done()
	for {
		select {
		case <-m.lifetime.Done():
			m.rejectPendingReloads()
			return
		default:
		}
		// Prefer an ordinary request when both queues are ready. At most one
		// compensating reload runs between ordinary requests, avoiding starvation.
		select {
		case req := <-m.reloadCh:
			m.runReloadRequest(req)
		default:
			select {
			case <-m.lifetime.Done():
				m.rejectPendingReloads()
				return
			case req := <-m.reloadCh:
				m.runReloadRequest(req)
			case <-m.overflowResyncCh:
				m.runOverflowResync()
				continue
			}
		}
		select {
		case <-m.overflowResyncCh:
			m.runOverflowResync()
		default:
		}
	}
}

func (m *Manager[T]) runOverflowResync() {
	// A failed refresh preserves the old state and reports through Errors; do
	// not spin retrying invalid input. Resume requires an explicit Reload.
	if m.eventsPaused.Load() || m.lifetime.Err() != nil {
		return
	}
	m.runReloadRequest(reloadRequest{trigger: TriggerProvider, reason: "provider:resync"})
}

func (m *Manager[T]) runReloadRequest(req reloadRequest) {
	pipeCtx := req.ctx
	if pipeCtx == nil {
		pipeCtx = m.lifetime
	}
	// Preserve caller deadlines/values and cancel in-flight work on shutdown.
	pipeCtx, cancel := context.WithCancel(pipeCtx)
	stop := context.AfterFunc(m.lifetime, cancel)
	err := pipeCtx.Err()
	if err == nil {
		// AfterFunc may not have run yet when shutdown already canceled
		// lifetime. Skip queued work, including context-free apply hooks.
		err = m.lifetime.Err()
	}
	// started is false when the request's context ended, or the manager
	// closed, before the writer reached it.
	started := err == nil
	if started {
		err = m.execReloadRequest(pipeCtx, req)
	}
	stop()
	cancel()
	// A request that never ran is not a reload failure: its caller already
	// receives the context error from submitAndWait, and publishing it would
	// evict real failures from the bounded channel.
	if err != nil && started {
		m.publishReloadError(req.trigger, req.reason, err)
	}
	if req.doneCh != nil {
		req.doneCh <- err
	}
}

// execReloadRequest runs one started request. Pipeline stages name their own
// panics; this recovers the rest (Plan assembly, rollback, tracers) so a panic
// fails the request instead of the process.
func (m *Manager[T]) execReloadRequest(ctx context.Context, req reloadRequest) (err error) {
	defer recoverPanic(ctx, m.opts.Logger, string(req.trigger)+" request", &err)
	if req.run != nil {
		return req.run(ctx)
	}
	return m.reload(ctx, req.trigger, req.reason, req.key)
}

// Errors reports failed reloads, plans and rollbacks that the writer ran,
// including errors also returned synchronously and cancellations that reached
// running work. Argument errors rejected before enqueueing and requests whose
// context ended (or whose manager closed) before the writer started them are
// excluded; their callers receive the error directly. The bounded channel
// evicts the oldest error on overflow and closes during Close. A nil Manager
// returns a closed channel.
func (m *Manager[T]) Errors() <-chan ReloadError {
	if m == nil {
		return closedErrCh
	}
	return m.errorsCh
}

func (m *Manager[T]) publishReloadError(trigger ReloadTrigger, reason string, err error) {
	if err == nil {
		return
	}
	// Only the reload goroutine (reloadLoop, and subscriber callbacks it
	// runs) publishes, so no send can race Close's close(errorsCh).
	re := fcerr.ReloadError{Err: err, Reason: reason, Trigger: trigger, At: time.Now()}
	for {
		select {
		case m.errorsCh <- re:
			return
		default:
		}
		select {
		case <-m.errorsCh:
		default:
		}
	}
}

// closedErrCh is returned by Errors() on a nil manager, avoiding a per-call
// allocation.
var closedErrCh = func() chan ReloadError {
	ch := make(chan ReloadError)
	close(ch)
	return ch
}()

const errorsChanCap = 16
