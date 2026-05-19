package fastconf

import (
	"fmt"
	"io"

	"github.com/fastabc/fastconf/internal/fcerr"
	iobs "github.com/fastabc/fastconf/internal/obs"
)

type MetricsSink = iobs.MetricsSink
type ProviderMetricsSink = iobs.ProviderMetricsSink
type StageMetricsSink = iobs.StageMetricsSink
type RenderMetricsSink = iobs.RenderMetricsSink
type DiffReporterMetricsSink = iobs.DiffReporterMetricsSink

type Tracer = iobs.Tracer
type Span = iobs.Span

type AuditSink = iobs.AuditSink
type AuditSinkFunc = iobs.AuditSinkFunc
type JSONAuditSink = iobs.JSONAuditSink

func NewJSONAuditSink(w io.Writer) *JSONAuditSink {
	return iobs.NewJSONAuditSink(w)
}

// WithMetrics installs a MetricsSink. Passing nil records a deferred
// error so a missing sink fails loudly at New() rather than silently
// dropping every metric.
func WithMetrics(m MetricsSink) Option {
	return func(o *options) {
		if m == nil {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithMetrics(nil)", fcerr.ErrFastConf))
			return
		}
		o.Metrics = iobs.NewMetricsBridge(m)
	}
}

// WithTracer installs a Tracer. Passing nil records a deferred error so
// a missing tracer fails loudly at New() rather than silently dropping
// every span.
func WithTracer(t Tracer) Option {
	return func(o *options) {
		if t == nil {
			o.DeferredErrs = append(o.DeferredErrs,
				fmt.Errorf("%w: WithTracer(nil)", fcerr.ErrFastConf))
			return
		}
		o.Tracer = t
	}
}

func WithAuditSink(sink AuditSink) Option {
	return func(o *options) {
		if sink != nil {
			o.AuditSinks = append(o.AuditSinks, sink)
		}
	}
}
