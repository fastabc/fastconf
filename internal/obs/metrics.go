package obs

import "time"

// MetricsSink is the minimal interface fastconf calls during reload.
type MetricsSink interface {
	ReloadStarted()
	ReloadFinished(ok bool, dur time.Duration)
	StateGeneration(gen uint64)
	LayersTotal(n int)
}

// ProviderMetricsSink is an optional extension for provider-watch lifecycle
// counters.
type ProviderMetricsSink interface {
	ProviderError(provider string)
	EventDropped(provider string)
}

// StageMetricsSink is an optional extension for per-stage timings.
type StageMetricsSink interface {
	StageDuration(stage string, dur time.Duration, ok bool)
}

// RenderMetricsSink is an optional extension for integrations/render errors.
type RenderMetricsSink interface {
	RenderError(name string)
}

// DiffReporterMetricsSink is an optional extension for DiffReporter queue
// backpressure telemetry.
type DiffReporterMetricsSink interface {
	DiffReporterQueueDepth(reporter string, depth, capacity int)
}

type NoopMetrics struct{}

func (NoopMetrics) ReloadStarted()                                  {}
func (NoopMetrics) ReloadFinished(_ bool, _ time.Duration)          {}
func (NoopMetrics) StateGeneration(_ uint64)                        {}
func (NoopMetrics) LayersTotal(_ int)                               {}
func (NoopMetrics) ProviderError(_ string)                          {}
func (NoopMetrics) EventDropped(_ string)                           {}
func (NoopMetrics) StageDuration(_ string, _ time.Duration, _ bool) {}
func (NoopMetrics) RenderError(_ string)                            {}
func (NoopMetrics) DiffReporterQueueDepth(_ string, _ int, _ int)   {}

// MetricsBridge routes optional metric extension methods when implemented
// by the installed sink.
type MetricsBridge struct {
	MetricsSink
	prov     ProviderMetricsSink
	stage    StageMetricsSink
	render   RenderMetricsSink
	diffRptr DiffReporterMetricsSink
}

func NewMetricsBridge(s MetricsSink) MetricsBridge {
	b := MetricsBridge{MetricsSink: s}
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

func (b MetricsBridge) ProviderError(name string) {
	if b.prov != nil {
		b.prov.ProviderError(name)
	}
}

func (b MetricsBridge) EventDropped(name string) {
	if b.prov != nil {
		b.prov.EventDropped(name)
	}
}

func (b MetricsBridge) StageDuration(stage string, dur time.Duration, ok bool) {
	if b.stage != nil {
		b.stage.StageDuration(stage, dur, ok)
	}
}

func (b MetricsBridge) RenderError(name string) {
	if b.render != nil {
		b.render.RenderError(name)
	}
}

func (b MetricsBridge) DiffReporterQueueDepth(reporter string, depth, capacity int) {
	if b.diffRptr != nil {
		b.diffRptr.DiffReporterQueueDepth(reporter, depth, capacity)
	}
}
