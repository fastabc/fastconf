package main

import (
	"context"
	"sync"

	"github.com/fastabc/fastconf"
)

// eventBus is a tiny pub/sub fed by the manager's Committed events; SSE
// subscribers read from a buffered channel each.
type eventBus struct {
	mu   sync.Mutex
	subs map[chan fastconf.ReloadCause]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan fastconf.ReloadCause]struct{})}
}

// Observe implements fastconf.Observer.
func (b *eventBus) Observe(_ context.Context, e fastconf.Event) {
	if c, ok := e.(fastconf.Committed); ok {
		b.publish(c.Cause)
	}
}

// publish disconnects a subscriber whose buffer is full rather than dropping
// the cause silently: closing its channel ends the SSE stream, and the client
// reconnects and reads the current state.
func (b *eventBus) publish(cause fastconf.ReloadCause) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.subs {
		select {
		case c <- cause:
		default:
			delete(b.subs, c)
			close(c)
		}
	}
}

func (b *eventBus) subscribe() chan fastconf.ReloadCause {
	c := make(chan fastconf.ReloadCause, 8)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c
}

// unsubscribe closes c unless publish already disconnected it.
func (b *eventBus) unsubscribe(c chan fastconf.ReloadCause) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[c]; ok {
		delete(b.subs, c)
		close(c)
	}
}
