package fastconf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/fcerr"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// WithNotifier appends a notification consumer owned by the Manager lifetime.
// Load ignores it. Consumers should use refresh's result before acknowledging.
func WithNotifier(n contracts.Notifier) Option {
	return func(o *options) {
		if n == nil {
			o.OptionErrs = append(o.OptionErrs, fmt.Errorf("%w: WithNotifier: nil notifier", fcerr.ErrFastConf))
			return
		}
		o.Notifiers = append(o.Notifiers, n)
	}
}

// WithResync periodically reloads to repair missed notifications. Zero disables
// reconciliation; Load ignores it. The interval must not be negative.
func WithResync(interval time.Duration) Option {
	return func(o *options) {
		if interval < 0 {
			o.OptionErrs = append(o.OptionErrs, fmt.Errorf("%w: WithResync: negative interval", fcerr.ErrFastConf))
			return
		}
		o.ResyncInterval = interval
	}
}

var errNotifierStopped = errors.New("fastconf: notifier stopped")

func (m *Manager[T]) startNotifiers(ctx context.Context) {
	for i, n := range m.opts.Notifiers {
		name := fmt.Sprintf("notify[%d]", i)
		if named, ok := n.(interface{ Name() string }); ok {
			name = "notify:" + named.Name()
		}
		m.bgWG.Add(1)
		go m.runNotifier(ctx, n, name)
	}
	if m.opts.ResyncInterval > 0 {
		m.bgWG.Add(1)
		go m.runResync(ctx)
	}
}
func (m *Manager[T]) runNotifier(ctx context.Context, n contracts.Notifier, name string) {
	defer m.bgWG.Done()
	backoff := providerutil.Backoff{Min: 250 * time.Millisecond, Max: 30 * time.Second}
	refresh := func(work context.Context) error {
		if m.Paused() {
			return nil
		}
		return m.requestReload(work, name, "")
	}
	for ctx.Err() == nil {
		started := time.Now()
		err := callNotifier(ctx, n, refresh)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errNotifierStopped
		}
		m.emit(ProviderError{Provider: name, Err: err})
		m.opts.Logger.LogAttrs(ctx, slog.LevelWarn, "fastconf notifier stopped", slog.String("provider", name), slog.Any("err", err))
		if time.Since(started) >= 30*time.Second {
			backoff.Reset()
		}
		if !sleepCtx(ctx, jitter(backoff.Next())) {
			return
		}
	}
}
func callNotifier(ctx context.Context, n contracts.Notifier, refresh func(context.Context) error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("fastconf: notifier panic: %v", v)
		}
	}()
	return n.Run(ctx, refresh)
}
func (m *Manager[T]) runResync(ctx context.Context) {
	defer m.bgWG.Done()
	interval := m.opts.ResyncInterval
	for {
		delay := interval
		if spread := interval / 5; spread > 0 {
			extra := time.Duration(rand.Int64N(int64(spread)))
			if interval <= time.Duration(1<<63-1)-extra {
				delay += extra
			}
		}
		if !sleepCtx(ctx, delay) {
			return
		}
		if m.Paused() {
			continue
		}
		if err := m.requestReload(ctx, "resync", ""); errors.Is(err, ErrClosed) {
			return
		}
	}
}
