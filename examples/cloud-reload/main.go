// Command cloud-reload demonstrates manager-owned notification and recovery workers.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
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

// Replace this consumer with SNS, Pub/Sub or Service Bus in production.
// Returning once simulates a disconnect; Manager supervises the restart.
type consumer struct {
	runs    atomic.Int32
	trigger chan struct{}
	results chan error
}

func (n *consumer) Run(ctx context.Context, refresh func(context.Context) error) error {
	if n.runs.Add(1) == 1 {
		return errors.New("demo disconnect")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.trigger:
			err := refresh(ctx)
			// ACK only after nil; on failure leave the event available for redelivery.
			select {
			case n.results <- err:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func run(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &source{}
	s.value.Store(1)
	n := &consumer{trigger: make(chan struct{}, 1), results: make(chan error, 1)}
	type config struct {
		Value int `json:"value"`
	}
	m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(s),
		fastconf.WithNotifier(n), fastconf.WithResync(10*time.Millisecond))
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	updated := make(chan struct{}, 1)
	defer fastconf.Subscribe(m, func(c *config) *int { return &c.Value }, func(_, next *int) {
		if *next == 2 {
			select {
			case updated <- struct{}{}:
			default:
			}
		}
	})()
	s.value.Store(2) // Notification is deliberately lost; periodic resync repairs it.
	select {
	case <-updated:
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := fmt.Fprintf(out, "recovered without notification: %d\n", m.Get().Value); err != nil {
		return err
	}

	m.Pause()
	// Pause does not drain work already queued. A Plan is a writer barrier here;
	// this fixture has no other producers besides the paused periodic worker.
	if _, err := m.Plan(ctx); err != nil {
		return err
	}
	s.value.Store(3)
	deliver := func() error {
		select {
		case n.trigger <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-n.results:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := deliver(); !errors.Is(err, fastconf.ErrPaused) {
		return fmt.Errorf("expected paused refresh, got %v", err)
	}
	if _, err := fmt.Fprintln(out, "paused: acknowledged=false"); err != nil {
		return err
	}
	m.Resume()
	if err := deliver(); err != nil {
		return err
	}
	if err := m.Close(); err != nil {
		return err
	} // Join both workers before closing borrowed clients.
	_, err = fmt.Fprintf(out, "redelivered: value=%d acknowledged=true restarted=%t\n", m.Get().Value, n.runs.Load() >= 2)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
