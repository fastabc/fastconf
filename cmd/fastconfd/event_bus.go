package main

import (
	"context"
	"sync"
	"time"

	"github.com/fastabc/fastconf"
)

// eventBus is a tiny pub/sub fed by the AuditSink contract; SSE
// subscribers read from a buffered channel each.
type eventBus struct {
	mu       sync.Mutex
	subs     map[chan fastconf.ReloadCause]struct{}
	lastOK   bool
	lastTime time.Time
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan fastconf.ReloadCause]struct{})}
}

// Audit implements fastconf.AuditSink.
func (b *eventBus) Audit(_ context.Context, cause fastconf.ReloadCause) error {
	b.mu.Lock()
	b.lastOK = true
	b.lastTime = time.Now()
	subs := make([]chan fastconf.ReloadCause, 0, len(b.subs))
	for c := range b.subs {
		subs = append(subs, c)
	}
	b.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- cause:
		default:
		}
	}
	return nil
}

func (b *eventBus) subscribe() chan fastconf.ReloadCause {
	c := make(chan fastconf.ReloadCause, 8)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c
}

func (b *eventBus) unsubscribe(c chan fastconf.ReloadCause) {
	b.mu.Lock()
	delete(b.subs, c)
	b.mu.Unlock()
	close(c)
}
