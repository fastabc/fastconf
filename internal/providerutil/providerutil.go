package providerutil

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
)

// Doer executes an HTTP request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClient keeps credentials and replayable request bodies at their original
// endpoint. A zero timeout permits protocol-specific long polling with context.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ReadAllMax reads at most max+1 bytes and rejects an oversized body without
// retaining or decoding a truncated configuration document.
func ReadAllMax(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = contracts.DefaultMaxBodyBytes
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: limit=%d bytes", contracts.ErrConfigTooLarge, max)
	}
	return b, nil
}

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
