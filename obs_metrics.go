package fastconf

// Metrics surfaces stay dependency-free in the core package; concrete
// exporters live in opt-in submodules.

import "time"

// MetricsSink is the minimal interface fastconf calls during reload. The
// default implementation is no-op so that metrics impose zero dependency on
// the user. A Prometheus implementation is provided in the
// observability/metrics/prometheus sub-module.
type MetricsSink interface {
	ReloadStarted()
	ReloadFinished(ok bool, dur time.Duration)
	StateGeneration(gen uint64)
	LayersTotal(n int)
}

// ProviderMetricsSink is an optional extension implemented by sinks that
// also want to observe provider-watch lifecycle counters (provider errors
// and dropped events). The framework checks for this interface at runtime
// via a type assertion, so existing MetricsSink implementations remain
// compatible.
type ProviderMetricsSink interface {
	ProviderError(provider string)
	EventDropped(provider string)
}

// StageMetricsSink is an optional extension for sinks that want
// per-stage histograms (assemble, merge, migration, transform,
// decode, validate, commit). Sinks that don't implement it are
// transparently ignored.
type StageMetricsSink interface {
	StageDuration(stage string, dur time.Duration, ok bool)
}

// RenderMetricsSink (SMELL-1210) is the optional extension implemented
// by sinks that want to observe integrations/render failures. The
// framework calls RenderError once per failed render attempt; sinks
// that don't implement this surface are transparently ignored.
type RenderMetricsSink interface {
	RenderError(name string)
}

// DiffReporterMetricsSink is the optional extension implemented by sinks
// that want to observe the DiffReporter backpressure pool. The framework
// samples each reporter's (length, capacity) after every successful
// commit and after each enqueue, so a Prometheus gauge can show "how
// close are we to dropping events?". Sinks that don't implement it are
// transparently ignored.
//
// reporter is a stable identifier of the form "diff-reporter:<idx>"
// matching the EventDropped label used when drop-on-full fires.
type DiffReporterMetricsSink interface {
	DiffReporterQueueDepth(reporter string, depth, capacity int)
}

type noopMetrics struct{}

func (noopMetrics) ReloadStarted()                                       {}
func (noopMetrics) ReloadFinished(_ bool, _ time.Duration)               {}
func (noopMetrics) StateGeneration(_ uint64)                             {}
func (noopMetrics) LayersTotal(_ int)                                    {}
func (noopMetrics) ProviderError(_ string)                               {}
func (noopMetrics) EventDropped(_ string)                                {}
func (noopMetrics) StageDuration(_ string, _ time.Duration, _ bool)      {}
func (noopMetrics) RenderError(_ string)                                 {}
func (noopMetrics) DiffReporterQueueDepth(_ string, _ int, _ int)        {}

// metricsBridge wraps a user-supplied MetricsSink and routes the
// optional ProviderMetricsSink methods to it when implemented; otherwise
// the calls become no-ops. This keeps call sites unconditional.
type metricsBridge struct {
	MetricsSink
	prov     ProviderMetricsSink
	stage    StageMetricsSink
	render   RenderMetricsSink
	diffRptr DiffReporterMetricsSink
}

func newMetricsBridge(s MetricsSink) metricsBridge {
	b := metricsBridge{MetricsSink: s}
	if p, ok := s.(ProviderMetricsSink); ok {
		b.prov = p
	}
	if st, ok := s.(StageMetricsSink); ok {
		b.stage = st
	}
	if r, ok := s.(RenderMetricsSink); ok {
		b.render = r
	}
	if d, ok := s.(DiffReporterMetricsSink); ok {
		b.diffRptr = d
	}
	return b
}

func (b metricsBridge) ProviderError(name string) {
	if b.prov != nil {
		b.prov.ProviderError(name)
	}
}

func (b metricsBridge) EventDropped(name string) {
	if b.prov != nil {
		b.prov.EventDropped(name)
	}
}

func (b metricsBridge) StageDuration(stage string, dur time.Duration, ok bool) {
	if b.stage != nil {
		b.stage.StageDuration(stage, dur, ok)
	}
}

// RenderError forwards render-stage failures to the configured sink
// (SMELL-1210). No-op when the sink does not implement
// RenderMetricsSink.
func (b metricsBridge) RenderError(name string) {
	if b.render != nil {
		b.render.RenderError(name)
	}
}

// DiffReporterQueueDepth forwards the per-reporter (depth, capacity)
// sample to the sink when it implements DiffReporterMetricsSink.
func (b metricsBridge) DiffReporterQueueDepth(reporter string, depth, capacity int) {
	if b.diffRptr != nil {
		b.diffRptr.DiffReporterQueueDepth(reporter, depth, capacity)
	}
}
