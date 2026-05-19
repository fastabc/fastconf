package obs

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	istate "github.com/fastabc/fastconf/internal/state"
)

// AuditSink receives a ReloadCause every time the Manager publishes a new
// state.
type AuditSink interface {
	Audit(ctx context.Context, cause istate.ReloadCause) error
}

// AuditSinkFunc adapts a free function into an AuditSink.
type AuditSinkFunc func(context.Context, istate.ReloadCause) error

func (f AuditSinkFunc) Audit(ctx context.Context, cause istate.ReloadCause) error {
	return f(ctx, cause)
}

// JSONAuditSink writes each cause as a single JSON line.
type JSONAuditSink struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

func NewJSONAuditSink(w io.Writer) *JSONAuditSink {
	if w == nil {
		w = os.Stderr
	}
	return &JSONAuditSink{w: w, enc: json.NewEncoder(w)}
}

func (s *JSONAuditSink) Audit(_ context.Context, cause istate.ReloadCause) error {
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
