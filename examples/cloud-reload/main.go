package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// missedConsumer stays healthy while an upstream notification is lost.
type missedConsumer struct{}

func (missedConsumer) Run(ctx context.Context, _ func(context.Context) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func example() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &source{}
	s.value.Store(1)
	type config struct {
		Value int `json:"value"`
	}
	m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(s),
		fastconf.WithNotifier(missedConsumer{}), fastconf.WithResync(10*time.Millisecond))
	if err != nil {
		return err
	}
	defer func() {
		if err := m.Close(); err != nil {
			slog.Warn("manager close failed", "error", err)
		}
	}()
	updated := make(chan struct{}, 1)
	defer fastconf.Subscribe(m, func(c *config) *int { return &c.Value }, func(_, next *int) {
		if *next == 2 {
			select {
			case updated <- struct{}{}:
			default:
			}
		}
	})()
	s.value.Store(2) // The notification is lost; periodic resync repairs it.
	select {
	case <-updated:
		fmt.Println("recovered without notification:", m.Get().Value)
		return nil
	case <-ctx.Done():
		return errors.New("periodic reload did not recover the missed notification")
	}
}

func main() {
	if err := example(); err != nil {
		panic(err)
	}
}
