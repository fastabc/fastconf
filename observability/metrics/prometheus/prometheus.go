// Package prometheus is the first-party Prometheus adapter for FastConf.
//
// It is shipped as a **separate Go module** so that users of the core
// fastconf package never pull in github.com/prometheus/client_golang
// transitively. Import the subpackage explicitly to opt in:
//
//	import promfc "github.com/fastabc/fastconf/metrics/prometheus"
//	sink := promfc.New(prometheus.DefaultRegisterer)
//	mgr, _ := fastconf.New[Config](ctx, fastconf.WithMetrics(sink))
//
// The Sink implements both fastconf.MetricsSink (reload counters,
// state generation, layer count) and the optional
// fastconf.ProviderMetricsSink (per-provider error / dropped event
// counters introduced in Phase 5).
package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Sink registers the standard FastConf metrics on the supplied
// Registerer. Pass prometheus.DefaultRegisterer for the global scrape
// endpoint, or a dedicated registry for tests.
type Sink struct {
	reloadTotal  *prometheus.CounterVec
	reloadDur    prometheus.Histogram
	gen          prometheus.Gauge
	layers       prometheus.Gauge
	provError    *prometheus.CounterVec
	eventDropped *prometheus.CounterVec
	stageDur     *prometheus.HistogramVec
}

// New registers the metric vectors on reg and returns a ready Sink.
// Calling New twice on the same registerer panics via MustRegister; use
// distinct registries in tests.
func New(reg prometheus.Registerer) *Sink {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	s := &Sink{
		reloadTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fastconf_reload_total",
			Help: "Total fastconf reloads, partitioned by result (ok|error).",
		}, []string{"result"}),
		reloadDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "fastconf_reload_duration_seconds",
			Help:    "Wall-clock time of a fastconf reload pipeline.",
			Buckets: prometheus.DefBuckets,
		}),
		gen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "fastconf_state_generation",
			Help: "Monotonically increasing generation of the active *State[T].",
		}),
		layers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "fastconf_layers_total",
			Help: "Number of layers participating in the most recent reload.",
		}),
		provError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fastconf_provider_error_total",
			Help: "Errors observed by Provider.Watch loops, by provider name.",
		}, []string{"provider"}),
		eventDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fastconf_event_dropped_total",
			Help: "Provider events dropped because the reload queue was full.",
		}, []string{"provider"}),
		stageDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "fastconf_stage_duration_seconds",
			Help:    "Per-stage reload pipeline latency (assemble, merge, migration, transform, decode, validate, commit).",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		}, []string{"stage", "result"}),
	}
	reg.MustRegister(s.reloadTotal, s.reloadDur, s.gen, s.layers, s.provError, s.eventDropped, s.stageDur)
	return s
}

// ReloadStarted satisfies fastconf.MetricsSink.
func (s *Sink) ReloadStarted() {}

// ReloadFinished satisfies fastconf.MetricsSink.
func (s *Sink) ReloadFinished(ok bool, dur time.Duration) {
	label := "ok"
	if !ok {
		label = "error"
	}
	s.reloadTotal.WithLabelValues(label).Inc()
	s.reloadDur.Observe(dur.Seconds())
}

// StateGeneration satisfies fastconf.MetricsSink.
func (s *Sink) StateGeneration(gen uint64) { s.gen.Set(float64(gen)) }

// LayersTotal satisfies fastconf.MetricsSink.
func (s *Sink) LayersTotal(n int) { s.layers.Set(float64(n)) }

// ProviderError satisfies fastconf.ProviderMetricsSink.
func (s *Sink) ProviderError(provider string) {
	s.provError.WithLabelValues(provider).Inc()
}

// EventDropped satisfies fastconf.ProviderMetricsSink.
func (s *Sink) EventDropped(provider string) {
	s.eventDropped.WithLabelValues(provider).Inc()
}

// StageDuration satisfies fastconf.StageMetricsSink (Phase 28).
// stage ∈ {"assemble","merge","migration","transform","decode","validate","commit"}.
func (s *Sink) StageDuration(stage string, dur time.Duration, ok bool) {
	label := "ok"
	if !ok {
		label = "error"
	}
	s.stageDur.WithLabelValues(stage, label).Observe(dur.Seconds())
}
