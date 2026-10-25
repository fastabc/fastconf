package fastconf

import (
	"context"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// reloadChanCap bounds pending work. Provider overflow retains one resync token;
// manual and coalesced file reloads wait for space or cancellation.
const reloadChanCap = 16

// reloadRequest is the unit consumed by reloadLoop.
//
// ctx propagates caller cancellation to providers, secret resolvers and stage
// boundaries. Hooks without a context must return before the writer can finish.
// Events enqueued directly by provider watchers leave ctx nil; reloadLoop uses
// the manager lifetime for those requests.
type reloadRequest struct {
	ctx     context.Context
	reason  string
	key     string                      // optional: parent dir for fs-driven reloads (audit dim)
	applyFn func(context.Context) error // if non-nil, called instead of m.reload (e.g. rollback)
	doneCh  chan error
}

// requestReload posts a reload request to the single-writer goroutine
// and waits for the result. Returns fcerr.ErrClosed if the manager has been
// closed, or ctx.Err() if the caller's context expires first.
//
// The caller's ctx is attached to the request so the pipeline itself
// (not just the wait) can be cancelled.
func (m *Manager[T]) requestReload(ctx context.Context, reason, key string) error {
	return m.enqueue(ctx, reloadRequest{reason: reason, key: key})
}

func (m *Manager[T]) enqueue(ctx context.Context, req reloadRequest) error {
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
			case <-m.resyncCh:
				m.resyncProvider()
				continue
			}
		}
		select {
		case <-m.resyncCh:
			m.resyncProvider()
		default:
		}
	}
}

func (m *Manager[T]) resyncProvider() {
	// A failed refresh preserves the old state and reports through Errors; do
	// not spin retrying invalid input. Resume requires an explicit Reload.
	if m.watchPaused.Load() || m.lifetime.Err() != nil {
		return
	}
	m.runReloadRequest(reloadRequest{reason: "provider:resync"})
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
	if err == nil {
		if req.applyFn != nil {
			err = req.applyFn(pipeCtx)
		} else {
			err = m.reload(pipeCtx, req.reason, req.key)
		}
	}
	stop()
	cancel()
	if err != nil {
		m.publishReloadError(req.reason, err)
	}
	if req.doneCh != nil {
		req.doneCh <- err
	}
}
