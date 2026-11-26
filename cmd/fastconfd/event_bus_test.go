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

// A subscriber whose buffer is full is disconnected instead of silently
// missing commits; its client reconnects and reads the current state.
func TestEventBusDisconnectsLaggingSubscriber(t *testing.T) {
	b := newEventBus()
	slow := b.subscribe()
	for i := 0; i < cap(slow)+1; i++ {
		b.publish(fastconf.ReloadCause{Reason: "r"})
	}
	for i := 0; i < cap(slow); i++ {
		if _, ok := <-slow; !ok {
			t.Fatalf("buffered cause %d lost", i)
		}
	}
	select {
	case _, ok := <-slow:
		if ok {
			t.Fatal("lagging subscriber got an extra cause; want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("lagging subscriber still open; want closed after an overflow")
	}
	b.unsubscribe(slow) // the handler still unsubscribes; must not double-close

	fresh := b.subscribe()
	defer b.unsubscribe(fresh)
	b.publish(fastconf.ReloadCause{Reason: "next"})
	if c := <-fresh; c.Reason != "next" {
		t.Fatalf("new subscriber got %+v", c)
	}
}
