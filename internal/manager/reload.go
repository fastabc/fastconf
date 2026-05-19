package manager

// Single-writer reload loop and the public Reload entry point.
// Manager owns one reloadCh and one reloadLoop goroutine; every
// trigger (Reload, fsnotify, provider watcher) is serialized through
// here so pipelines never interleave.

import (
	"context"
	"fmt"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/fcerr"
	istate "github.com/fastabc/fastconf/internal/state"
)

// Reload triggers a synchronous reload. On failure the previous state
// is preserved.
//
// Options:
//   - WithSourceOverride(map) injects a one-shot in-memory layer at the
//     top of the priority stack for this reload only. The map is consumed;
//     do not mutate it after the call.
//   - WithReloadReason(s) overrides the default "manual" reason tag used
//     for audit / metrics / logging.
func (m *M[T]) Reload(ctx context.Context, opts ...ReloadOption) error {
	cfg := reloadConfig{reason: "manual"}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.override == nil {
		return m.requestReload(ctx, cfg.reason)
	}
	select {
	case <-m.closed:
		return fcerr.ErrClosed
	default:
	}
	extra := stagedLayer{
		src: istate.SourceRef{
			Path:     "override://once",
			Kind:     istate.LayerProvider,
			Priority: contracts.BandFileBase + contracts.PriorityCLI,
		},
		data: cfg.override,
	}
	reason := cfg.reason
	if reason == "manual" {
		reason = "override"
	}
	req := reloadRequest{
		ctx:    ctx,
		reason: reason,
		doneCh: make(chan error, 1),
		applyFn: func(pipeCtx context.Context) error {
			return m.reloadWithExtra(pipeCtx, reason, extra)
		},
	}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReloadOption tunes a single Reload invocation.
type ReloadOption func(*reloadConfig)

type reloadConfig struct {
	reason   string
	override map[string]any
}

// WithSourceOverride attaches a one-shot in-memory layer to this reload,
// merged above CLI flags. The override map is deep-copied at the call
// site; callers may freely mutate or reuse the original after Reload
// returns. The layer is not remembered: a subsequent Reload reverts to
// the natural state.
//
// Use cases: targeted integration tests, ad-hoc operator overrides in
// fastconfctl, "rehearse a change without writing a file". Never use
// this from production hot paths.
func WithSourceOverride(override map[string]any) ReloadOption {
	return func(c *reloadConfig) { c.override = deepCopyMap(override) }
}

// deepCopyMap returns a fully-independent copy of m. Nested
// map[string]any and []any values are recursively cloned; all other
// values (strings, numbers, structs) are copied by value, which is
// safe for the in-memory configuration shapes that reach this path.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}

// WithReloadReason overrides the default "manual" reason tag stamped on
// the audit / metric / log lines this reload emits.
func WithReloadReason(reason string) ReloadOption {
	return func(c *reloadConfig) {
		if reason != "" {
			c.reason = reason
		}
	}
}

// reloadRequest is the unit consumed by reloadLoop.
//
// ctx carries the caller's pipeline context: it is propagated all the way
// through assemble + provider.Load + commit so a Reload(ctx)/Plan(ctx)
// cancellation actually aborts slow providers, secret resolvers, and
// transformers. Triggers without a caller (fsnotify, provider watchers)
// leave ctx nil; reloadLoop substitutes context.Background() in that case.
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
func (m *M[T]) requestReload(ctx context.Context, reason string) error {
	return m.requestReloadWithKey(ctx, reason, "")
}

// requestReloadWithKey is the variant used by the file-system watcher,
// where "key" is the parent directory whose burst triggered this reload.
// The key is surfaced in istate.ReloadCause for audit fan-out.
func (m *M[T]) requestReloadWithKey(ctx context.Context, reason, key string) error {
	select {
	case <-m.closed:
		return fcerr.ErrClosed
	default:
	}
	req := reloadRequest{ctx: ctx, reason: reason, key: key, doneCh: make(chan error, 1)}
	select {
	case m.reloadCh <- req:
	case <-m.closed:
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.doneCh:
		return err
	case <-m.closed:
		return fcerr.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rejectPendingReloads drains any already-enqueued reload requests and
// signals their waiters with fcerr.ErrClosed so Close never strands callers.
func (m *M[T]) rejectPendingReloads() {
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
func (m *M[T]) reloadLoop() {
	defer m.bgWG.Done()
	for {
		select {
		case <-m.closed:
			m.rejectPendingReloads()
			return
		default:
		}
		select {
		case <-m.closed:
			m.rejectPendingReloads()
			return
		case req := <-m.reloadCh:
			// Use the caller's pipeline context when supplied so a
			// Reload(ctx) cancellation actually aborts the pipeline.
			// Triggers without a caller (fsnotify, provider watcher)
			// leave req.ctx nil — fall back to Background.
			pipeCtx := req.ctx
			if pipeCtx == nil {
				pipeCtx = context.Background()
			}
			var err error
			if req.applyFn != nil {
				err = req.applyFn(pipeCtx)
			} else {
				err = m.reloadWithKey(pipeCtx, req.reason, req.key)
			}
			if err != nil {
				m.publishReloadError(req.reason, err)
			}
			if req.doneCh != nil {
				req.doneCh <- err
			}
		}
	}
}

// reloadWithExtra performs the full reload pipeline with an additional
// in-memory layer injected at the top. Failures preserve the previous
// state, identical to the regular reload path.
func (m *M[T]) reloadWithExtra(ctx context.Context, reason string, extra stagedLayer) error {
	staged, appendSlices, err := m.assemble(ctx, "")
	if err != nil {
		return fmt.Errorf("reload-with-source assemble: %w", err)
	}
	staged = append(staged, extra)
	return m.commit(ctx, staged, appendSlices, reason)
}
