// Package providerutil holds helpers shared by the root module's providers:
// a redirect-safe HTTP client, the latest-document holder for streaming
// providers, nonblocking event offers and retry backoff. ReadAllMax and
// SafeError are frozen forwarding shims for published v1 cloud modules.
package providerutil

import (
	"maps"
	"sync"
	"time"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
)

// Latest retains the most recently decoded streaming document. Callers must
// not mutate a stored map; Snapshot copies its top level and the manager
// deep-clones it before merging. The zero value is a stale, empty snapshot.
type Latest struct {
	mu       sync.RWMutex
	value    map[string]any
	revision string
	loaded   bool
}

// Store accepts a document and retains the last non-empty revision.
func (l *Latest) Store(value map[string]any, revision string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.value, l.loaded = value, true
	if revision != "" {
		l.revision = revision
	}
}

// Snapshot returns the latest document metadata and a shallow map copy.
func (l *Latest) Snapshot() contracts.Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	value := maps.Clone(l.value)
	if value == nil {
		value = map[string]any{}
	}
	return contracts.Snapshot{Map: value, Revision: l.revision, Stale: !l.loaded}
}

// Offer attempts a nonblocking event send; false reports a full channel.
func Offer(ch chan<- contracts.Event, ev contracts.Event) bool {
	select {
	case ch <- ev:
		return true
	default:
		return false
	}
}

// GraftAt wraps inner below root, or returns inner when root is empty.
func GraftAt(inner map[string]any, root []string) map[string]any {
	if len(root) == 0 {
		return inner
	}
	out := map[string]any{}
	confmap.Set(out, root, inner)
	return out
}

// MaybeCoerce converts scalar strings only when enabled.
func MaybeCoerce(s string, enabled bool) any {
	if !enabled {
		return s
	}
	return confmap.Coerce(s, confmap.CoerceOptions{IgnoreCase: true})
}

// Backoff doubles Min up to Max; callers must set 0 < Min <= Max.
// It is owned by one retry loop.
type Backoff struct{ Min, Max, cur time.Duration }

// Next returns Min first, then doubles each delay up to Max.
func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.Min
	}
	d := b.cur
	if b.cur >= b.Max-b.cur {
		b.cur = b.Max
	} else {
		b.cur *= 2
	}
	return d
}

// Reset makes the next delay Min again.
func (b *Backoff) Reset() { b.cur = 0 }
