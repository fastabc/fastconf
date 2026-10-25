package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
)

type source struct{ value atomic.Int32 }

func (*source) Name() string { return "local-cloud" }
func (s *source) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"value": int(s.value.Load())}}, nil
}

// Application-owned supervision: SDK retries handle requests; this loop
// restarts a terminated consumer. Add alerting for persistent failures.
func supervise(ctx context.Context, run func(context.Context) error) {
	for ctx.Err() == nil {
		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("notification consumer stopped; retrying", "error", err)
		timer := time.NewTimer(time.Second + time.Duration(rand.Int64N(int64(time.Second))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Periodic reconciliation repairs missed events, with jitter to spread load.
func reconcile(ctx context.Context, reload func(context.Context) error, interval time.Duration) {
	for {
		timer := time.NewTimer(interval + time.Duration(rand.Int64N(int64(interval/5))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		// Keep each attempt bounded even while the service context is long-lived.
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := reload(work)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("periodic reload failed", "error", err)
		}
	}
}

func example() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish the initial value before notification and reconciliation workers start.
	s := &source{}
	s.value.Store(1)
	type config struct {
		Value int `json:"value"`
	}
	m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(s))
	if err != nil {
		return err
	}
	defer func() {
		if err := m.Close(); err != nil {
			slog.Warn("manager close failed", "error", err)
		}
	}()

	// Simulate an upstream change whose notification never reaches this process.
	s.value.Store(2) // The only upstream mutation; its notification is lost.
	updated := make(chan struct{}, 1)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		supervise(ctx, func(ctx context.Context) error {
			// Production: return consumer.Run(ctx, func(work context.Context) error {
			//     return m.Reload(work)
			// }). Reload has variadic options, so it needs this small wrapper.
			<-ctx.Done() // Simulate a healthy receiver with a missed event.
			return ctx.Err()
		})
	}()

	// This independent worker supplies the recovery path for the missed event.
	go func() {
		defer workers.Done()
		reconcile(ctx, func(work context.Context) error {
			if err := m.Reload(work); err != nil {
				return err
			}
			if m.Get().Value == 2 {
				select {
				case updated <- struct{}{}:
				default:
				}
			}
			return nil
		}, 10*time.Millisecond) // Production: e.g. time.Minute, adjusted to quotas.
	}()

	// Wait for convergence, then join both workers before closing borrowed resources.
	select {
	case <-updated:
	case <-ctx.Done():
		err = errors.New("periodic reload did not recover the missed notification")
	}

	cancel()
	workers.Wait() // Stop receivers before closing Manager and SDK clients.
	if err == nil {
		fmt.Println("recovered without notification:", m.Get().Value)
	}
	return err
}

func main() {
	if err := example(); err != nil {
		panic(err)
	}
}
