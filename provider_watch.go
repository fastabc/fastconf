package fastconf

// Provider event subscription, resume tracking, and exponential-backoff
// re-subscribe. Each Provider whose Watch() returns a non-nil channel
// gets one goroutine. Events are coalesced through the existing
// serialized reloadCh (single-writer invariant). If the queue is full
// the event is DROPPED (counted via metrics) rather than block.

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// jitter returns d + uniform[0, d/2) so that many replicas restarting
// at the same moment do not all reconnect on the same tick.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(int64(d/2)+1))
}

// errResumeGap is the ProviderError cause for a provider that resubscribed
// without honoring the requested revision (Event.Gap).
var errResumeGap = errors.New("fastconf: provider resumed with a gap; changes may have been missed")

// resumeState tracks the last revision observed per provider; it is passed
// back to Provider.Watch so a provider can pick up where it left off after
// a transient disconnect. The framework stores this state in-process only;
// the revisions reach observers on Committed.Cause.Revisions.
type resumeState struct {
	mu   sync.Mutex
	revs map[string]string
}

func newResumeState() *resumeState { return &resumeState{revs: map[string]string{}} }

func (r *resumeState) get(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revs[name]
}

func (r *resumeState) set(name, rev string) {
	if rev == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revs[name] = rev
}

// startProviderWatchers spins up one goroutine per provider whose Watch()
// method returns a non-nil channel. Events are coalesced through the
// existing serialized reloadCh — if the queue is full we DROP the event
// (counted via metrics) rather than block, preserving the single-writer
// invariant and bounded memory.
//
// Provider Watch failures are logged and retried with exponential backoff
// (250ms .. 30s) so a flaky remote source cannot tight-loop the CPU.
func (m *Manager[T]) startProviderWatchers(ctx context.Context) {
	for _, e := range m.opts.Providers {
		m.bgWG.Add(1)
		go m.runProviderWatcher(ctx, e.Provider)
	}
}

func (m *Manager[T]) runProviderWatcher(ctx context.Context, p contracts.Provider) {
	defer m.bgWG.Done()
	const minDelay, maxDelay = 250 * time.Millisecond, 30 * time.Second
	delay := minDelay
	for {
		if ctx.Err() != nil {
			return
		}
		ch, err := p.Watch(ctx, m.resume.get(p.Name()))
		if err != nil {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider watch error", slog.String("provider", p.Name()), slog.Any("err", err), slog.Duration("retry_in", delay))
			m.emit(ProviderError{Provider: p.Name(), Err: err})
			if !m.sleep(ctx, jitter(delay)) {
				return
			}
			if delay < maxDelay {
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			}
			continue
		}
		if ch == nil {
			// Provider explicitly opts out of watching — exit goroutine.
			return
		}
		// Reset backoff after a successful subscribe.
		delay = minDelay
		m.consumeProviderEvents(ctx, p, ch)
		// consumeProviderEvents returns when ch closes or ctx is canceled;
		// loop to resubscribe with backoff if appropriate.
		if ctx.Err() != nil {
			return
		}
		if !m.sleep(ctx, jitter(delay)) {
			return
		}
	}
}

func (m *Manager[T]) consumeProviderEvents(ctx context.Context, p contracts.Provider, ch <-chan contracts.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Revision != "" {
				m.resume.set(p.Name(), ev.Revision)
			}
			if ev.Gap {
				// The provider could not resume from the revision we asked
				// for; changes in between may have been missed. The reload
				// below catches up, the metric records the gap.
				m.emit(ProviderError{Provider: p.Name(), Err: errResumeGap})
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider resumed with a gap", slog.String("provider", p.Name()), slog.String("revision", ev.Revision))
			}
			if m.watchPaused.Load() {
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "fastconf provider event ignored (watch paused)", slog.String("provider", p.Name()), slog.String("reason", ev.Reason))
				continue
			}
			reason := "provider:" + p.Name()
			if ev.Reason != "" {
				var sb strings.Builder
				sb.WriteString("provider:")
				sb.WriteString(p.Name())
				sb.WriteByte(':')
				sb.WriteString(ev.Reason)
				reason = sb.String()
			}
			req := reloadRequest{reason: reason}
			select {
			case m.reloadCh <- req:
				// Fire-and-forget: doneCh is nil for provider-triggered reloads.
				// reloadLoop checks doneCh != nil before sending.
			default:
				m.emit(EventDropped{Source: p.Name()})
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider event dropped (queue full)", slog.String("provider", p.Name()), slog.String("reason", ev.Reason))
			}
		}
	}
}

// sleep returns false if the context is canceled. Used by
// runProviderWatcher for backoff between resubscribe attempts.
func (m *Manager[T]) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
