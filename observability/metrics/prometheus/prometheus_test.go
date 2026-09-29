package prometheus

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSink_RecordsAllSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := New(reg)

	s.ReloadStarted()
	s.ReloadFinished(true, 5*time.Millisecond)
	s.ReloadFinished(false, 1*time.Millisecond)
	s.StateGeneration(7)
	s.LayersTotal(4)
	s.ProviderError("vault")
	s.EventDropped("consul")

	if got := testutil.ToFloat64(s.reloadTotal.WithLabelValues("ok")); got != 1 {
		t.Fatalf("reload_total{ok}=%v want 1", got)
	}
	if got := testutil.ToFloat64(s.reloadTotal.WithLabelValues("error")); got != 1 {
		t.Fatalf("reload_total{error}=%v want 1", got)
	}
	if got := testutil.ToFloat64(s.gen); got != 7 {
		t.Fatalf("state_generation=%v want 7", got)
	}
	if got := testutil.ToFloat64(s.layers); got != 4 {
		t.Fatalf("layers_total=%v want 4", got)
	}
	if got := testutil.ToFloat64(s.provError.WithLabelValues("vault")); got != 1 {
		t.Fatalf("provider_error{vault}=%v want 1", got)
	}
	if got := testutil.ToFloat64(s.eventDropped.WithLabelValues("consul")); got != 1 {
		t.Fatalf("event_dropped{consul}=%v want 1", got)
	}
}

func TestNew_NilRegistererUsesDefault(t *testing.T) {
	previous := prometheus.DefaultRegisterer
	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = reg
	t.Cleanup(func() { prometheus.DefaultRegisterer = previous })
	s := New(nil)
	s.StateGeneration(3)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "fastconf_state_generation" && family.GetMetric()[0].GetGauge().GetValue() == 3 {
			return
		}
	}
	t.Fatal("nil registerer did not use the default registry")
}

func TestSink_StageDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := New(reg)

	s.StageDuration("merge", 250*time.Microsecond, true)
	s.StageDuration("merge", 1*time.Millisecond, true)
	s.StageDuration("validate", 5*time.Millisecond, false)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() != "fastconf_stage_duration_seconds" {
			continue
		}
		found = true
		// Expect 2 series (merge ok, validate error).
		if len(mf.GetMetric()) != 2 {
			t.Fatalf("want 2 stage series, got %d", len(mf.GetMetric()))
		}
	}
	if !found {
		t.Fatal("fastconf_stage_duration_seconds not registered")
	}
}

func TestAlertRulesDocumentCriticalSignals(t *testing.T) {
	b, err := os.ReadFile("alert_rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		"FastConfReloadFailures",
		"FastConfReloadLatencyHigh",
		"FastConfProviderWatchErrors",
		"FastConfProviderEventsDropped",
		"fastconf:reload_success_ratio_5m",
		"fastconf:reload_p99_5m",
		"fastconf:provider_error_rate_5m",
		"fastconf:event_dropped_rate_5m",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("alert_rules.yaml missing %q", want)
		}
	}
}

func TestNew_DuplicateRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = New(reg)
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	_ = New(reg)
}
