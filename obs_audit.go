package fastconf

// Audit sinks run after a successful commit and receive the same ReloadCause
// that is stamped onto State[T].

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// AuditSink receives a ReloadCause every time the Manager publishes a
// new state. Implementations MUST be goroutine-safe and SHOULD return
// quickly — the manager invokes Audit synchronously in the reload
// goroutine, so a slow sink directly inflates publish latency.
type AuditSink interface {
	Audit(ctx context.Context, cause ReloadCause) error
}

// AuditSinkFunc adapts a free function into an AuditSink.
type AuditSinkFunc func(context.Context, ReloadCause) error

// Audit implements AuditSink.
func (f AuditSinkFunc) Audit(ctx context.Context, cause ReloadCause) error {
	return f(ctx, cause)
}

// JSONAuditSink writes each cause as a single JSON line to w. It is
// safe for concurrent use; writes are serialized through a mutex so
// individual lines never interleave. The encoder is created once and
// reused under the lock to avoid one allocation per Audit call.
type JSONAuditSink struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

// NewJSONAuditSink returns a sink that writes to w (defaults to
// os.Stderr when w is nil).
func NewJSONAuditSink(w io.Writer) *JSONAuditSink {
	if w == nil {
		w = os.Stderr
	}
	return &JSONAuditSink{w: w, enc: json.NewEncoder(w)}
}

// Audit implements AuditSink.
func (s *JSONAuditSink) Audit(_ context.Context, cause ReloadCause) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(struct {
		Reason    string            `json:"reason"`
		At        time.Time         `json:"at"`
		Revisions map[string]string `json:"revisions,omitempty"`
		Tenant    string            `json:"tenant,omitempty"`
	}{
		Reason:    cause.Reason,
		At:        time.Unix(0, cause.At),
		Revisions: cause.Revisions,
		Tenant:    cause.Tenant,
	})
}

// WithAuditSink installs an AuditSink invoked once per successful
// reload. May be combined freely with other Options; multiple
// WithAuditSink calls register multiple sinks (fan-out, in order).
func WithAuditSink(sink AuditSink) Option {
	return func(o *options) {
		if sink == nil {
			return
		}
		o.auditSinks = append(o.auditSinks, sink)
	}
}
