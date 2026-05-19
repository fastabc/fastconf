package testutil

import (
	"context"
	"sync"

	"github.com/fastabc/fastconf/contracts"
)

// RecordingTracer is a test-only Tracer that captures all started spans.
// It is safe for concurrent use.
type RecordingTracer struct {
	mu    sync.Mutex
	spans []*RecordingSpan
}

// Start records a new span with the given name and appends it to the internal
// list. It satisfies the iobs.Tracer interface (Start returns contracts.Span).
func (r *RecordingTracer) Start(ctx context.Context, name string) (context.Context, contracts.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sp := &RecordingSpan{Name: name, Attrs: map[string]any{}}
	r.spans = append(r.spans, sp)
	return ctx, sp
}

// Spans returns a snapshot copy of all recorded spans. Safe to call without
// holding any lock; the snapshot is independent of future mutations.
func (r *RecordingTracer) Spans() []*RecordingSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*RecordingSpan, len(r.spans))
	copy(out, r.spans)
	return out
}

// FindSpan returns the first recorded span whose Name equals name, or nil.
func (r *RecordingTracer) FindSpan(name string) *RecordingSpan {
	for _, sp := range r.Spans() {
		if sp.Name == name {
			return sp
		}
	}
	return nil
}

// RecordingSpan is a test-only Span that records End, RecordError, and
// SetAttribute calls for later assertion.
type RecordingSpan struct {
	Name  string
	Ended bool
	Err   error
	Attrs map[string]any
}

func (s *RecordingSpan) End() { s.Ended = true }

func (s *RecordingSpan) RecordError(err error) { s.Err = err }

func (s *RecordingSpan) SetAttribute(key string, value any) {
	if s.Attrs == nil {
		s.Attrs = map[string]any{}
	}
	s.Attrs[key] = value
}
