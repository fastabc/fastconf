package fastconf

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// jitter returns d + uniform[0, d/2) so that many replicas restarting
// at the same moment do not all reconnect on the same tick.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(int64(d/2)+1))
}

// Provider watch retry timing. These are variables only so tests can shorten
// them; production code never changes them.
var (
	providerWatchMinDelay = 250 * time.Millisecond
	providerWatchMaxDelay = 30 * time.Second
	// providerWatchHealthy is how long a subscription must stay open before
	// its end resets the backoff. It matches the notifier restart policy.
	providerWatchHealthy = 30 * time.Second
)

// errResumeGap is the ProviderError cause for a provider that resubscribed
// without honoring the requested revision (Event.Gap).
var errResumeGap = errors.New("fastconf: provider resumed with a gap; changes may have been missed")

// errWatchClosed is the ProviderError cause for a Watch channel that closed
// before the manager canceled it. contracts.Watcher reserves closing for
// cancellation, so the manager resubscribes with backoff.
var errWatchClosed = errors.New("fastconf: provider watch channel closed before cancellation")

// watchRetryDelay returns the wait before resubscribing after a stream that
// stayed open for lived. Only streams open for providerWatchHealthy reset the
// backoff, so a Watch that keeps returning short-lived channels cannot spin.
func watchRetryDelay(b *providerutil.Backoff, lived time.Duration) time.Duration {
	if lived >= providerWatchHealthy {
		b.Reset()
	}
	return b.Next()
}

// revisionTable tracks the last revision observed per provider; it is passed
// back to Watcher.Watch so a provider can pick up where it left off after
// a transient disconnect. The framework stores this state in-process only;
// the revisions reach observers on Committed.Cause.Revisions.
type revisionTable struct {
	mu   sync.Mutex
	revs map[string]string
}

func newRevisionTable() *revisionTable { return &revisionTable{revs: map[string]string{}} }

func (r *revisionTable) get(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revs[name]
}

func (r *revisionTable) set(name, rev string) {
	if rev == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revs[name] = rev
}

// startProviderWatchers runs one subscription loop per native Watcher.
// Triggers enter the writer queue, retaining one resync token on overflow.
// Watch errors and channels that close before cancellation emit ProviderError
// and resubscribe with 250 ms–30 s exponential backoff and jitter; the backoff
// resets only after a subscription has stayed open for 30 s.
func (m *Manager[T]) startProviderWatchers(ctx context.Context) {
	for _, e := range m.opts.Providers {
		watcher, ok := e.Provider.(contracts.Watcher)
		if !ok {
			continue
		}
		m.bgWG.Add(1)
		go m.runProviderWatcher(ctx, e.Provider, watcher)
	}
}

func (m *Manager[T]) runProviderWatcher(ctx context.Context, p contracts.Provider, watcher contracts.Watcher) {
	defer m.bgWG.Done()
	backoff := providerutil.Backoff{Min: providerWatchMinDelay, Max: providerWatchMaxDelay}
	for {
		if ctx.Err() != nil {
			return
		}
		ch, err := watcher.Watch(ctx, m.providerRevisions.get(p.Name()))
		if err != nil {
			delay := backoff.Next()
			m.opts.Logger.LogAttrs(ctx, slog.LevelWarn, "fastconf provider watch error", slog.String("provider", p.Name()), slog.Any("err", err), slog.Duration("retry_in", delay))
			m.emit(ProviderError{Provider: p.Name(), Err: err})
			if !sleepCtx(ctx, jitter(delay)) {
				return
			}

			continue
		}
		if ch == nil {
			// Provider explicitly opts out of watching — exit goroutine.
			return
		}
		// A successful subscribe does not reset the backoff by itself: a
		// Watch that returns a channel and closes it at once would otherwise
		// resubscribe several times per second forever.
		started := time.Now()
		m.consumeProviderEvents(ctx, p, ch)
		// consumeProviderEvents returns when ch closes or ctx is canceled;
		// loop to resubscribe with backoff if appropriate.
		if ctx.Err() != nil {
			return
		}
		lived := time.Since(started)
		delay := watchRetryDelay(&backoff, lived)
		m.opts.Logger.LogAttrs(ctx, slog.LevelWarn, "fastconf provider watch closed", slog.String("provider", p.Name()), slog.Duration("lived", lived), slog.Duration("retry_in", delay))
		m.emit(ProviderError{Provider: p.Name(), Err: errWatchClosed})
		if !sleepCtx(ctx, jitter(delay)) {
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
				m.providerRevisions.set(p.Name(), ev.Revision)
			}
			if ev.Gap {
				// The provider could not resume from the revision we asked
				// for; changes in between may have been missed. The reload
				// below catches up, the metric records the gap.
				m.emit(ProviderError{Provider: p.Name(), Err: errResumeGap})
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider resumed with a gap", slog.String("provider", p.Name()), slog.String("revision", ev.Revision))
			}
			if m.eventsPaused.Load() {
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
				// runReloadRequest checks doneCh != nil before sending.
			default:
				// Coalesce dropped triggers. Taking the token before Load lets
				// an overflow during that Load schedule another refresh.
				select {
				case m.overflowResyncCh <- struct{}{}:
				default:
				}
				m.emit(EventDropped{Source: p.Name()})
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf provider event dropped (queue full)", slog.String("provider", p.Name()), slog.String("reason", ev.Reason))
			}
		}
	}
}

// sleepCtx returns false if the context is canceled. Provider watchers,
// notifiers and periodic resync use it for backoff and interval waits.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
