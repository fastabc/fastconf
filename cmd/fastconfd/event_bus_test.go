package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
)

func TestEventBusPublishUnsubscribeRace(t *testing.T) {
	b := newEventBus()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c := b.subscribe()
				b.Observe(context.Background(), fastconf.Committed{Cause: fastconf.ReloadCause{Reason: "test"}})
				b.unsubscribe(c)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			b.Observe(context.Background(), fastconf.Committed{Cause: fastconf.ReloadCause{Reason: "broadcast"}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("event bus stalled")
	}
	wg.Wait()
}
