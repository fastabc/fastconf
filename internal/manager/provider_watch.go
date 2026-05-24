package manager

// Provider event subscription, resume tracking, and exponential-backoff
// re-subscribe. Each Provider whose Watch() returns a non-nil channel
// gets one goroutine. Events are coalesced through the existing
// serialized reloadCh (single-writer invariant). If the queue is full
// the event is DROPPED (counted via metrics) rather than block.

import (
	"context"
	"errors"
	"math/rand"
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
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

// resumeState tracks the last revision observed per provider so that a
// Resumable provider can pick up where it left off after a transient
// disconnect. The framework stores this state in-process only; durable
// resume across process restarts requires the user to wire the value
// from their AuditSink into ResumeStore on next start.
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
func (m *M[T]) startProviderWatchers(ctx context.Context) {
	for _, p := range m.opts.Providers {
		m.bgWG.Add(1)
		go m.runProviderWatcher(ctx, p)
	}
}

func (m *M[T]) runProviderWatcher(ctx context.Context, p contracts.Provider) {
	defer m.bgWG.Done()
	const minDelay, maxDelay = 250 * time.Millisecond, 30 * time.Second
	delay := minDelay
	for {
		select {
		case <-m.closed:
			return
		case <-ctx.Done():
			return
		default:
		}
		ch, err := m.subscribe(ctx, p)
		if err != nil {
			m.opts.Log.Warn().
				Str("provider", p.Name()).
				Err(err).
				Dur("retry_in", delay).
				Msg("fastconf provider watch error")
			m.opts.Metrics.ProviderError(p.Name())
			if !m.sleep(jitter(delay)) {
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
		// consumeProviderEvents returns when ch closes or ctx/closed
		// fires; loop to resubscribe with backoff if appropriate.
		select {
		case <-m.closed:
			return
		case <-ctx.Done():
			return
		default:
		}
		if !m.sleep(jitter(delay)) {
			return
		}
	}
}

// subscribe prefers Resumable.WatchFrom when the provider implements it
// AND we have a remembered revision. ErrResumeUnsupported transparently
// falls back to Watch and is recorded as a provider error so audit can
// surface the gap.
func (m *M[T]) subscribe(ctx context.Context, p contracts.Provider) (<-chan contracts.Event, error) {
	if r, ok := p.(contracts.Resumable); ok {
		if last := m.resume.get(p.Name()); last != "" {
			ch, err := r.WatchFrom(ctx, last)
			if err == nil {
				return ch, nil
			}
			if errors.Is(err, contracts.ErrResumeUnsupported) {
				m.opts.Metrics.ProviderError(p.Name())
				m.opts.Log.Warn().
					Str("provider", p.Name()).
					Str("last_rev", last).
					Msg("fastconf provider resume unsupported, falling back to cold watch")
				// fall through to plain Watch
			} else {
				return nil, err
			}
		}
	}
	return p.Watch(ctx)
}

func (m *M[T]) consumeProviderEvents(ctx context.Context, p contracts.Provider, ch <-chan contracts.Event) {
	for {
		select {
		case <-m.closed:
			return
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Revision != "" {
				m.resume.set(p.Name(), ev.Revision)
			}
			if m.watchPaused.Load() {
				m.opts.Log.Debug().
					Str("provider", p.Name()).
					Str("reason", ev.Reason).
					Msg("fastconf provider event ignored (watch paused)")
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
				m.opts.Metrics.EventDropped(p.Name())
				m.opts.Log.Warn().
					Str("provider", p.Name()).
					Str("reason", ev.Reason).
					Msg("fastconf provider event dropped (queue full)")
			}
		}
	}
}

// sleep returns false if the manager closed during the wait. Used by
// runProviderWatcher for backoff between resubscribe attempts.
func (m *M[T]) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-m.closed:
		return false
	}
}
