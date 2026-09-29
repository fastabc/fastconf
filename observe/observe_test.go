package observe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/observe"
)

func TestMultiSkipsNilInOrder(t *testing.T) {
	var got []int
	observe.Multi(nil, observe.Func(func(context.Context, fastconf.Event) { got = append(got, 1) }), nil,
		observe.Func(func(context.Context, fastconf.Event) { got = append(got, 2) })).Observe(context.Background(), fastconf.ReloadStarted{})
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatal(got)
	}
}

func TestAsyncCloseDrainsAndRejectsLateEvents(t *testing.T) {
	var count int
	a := observe.Async(observe.Func(func(context.Context, fastconf.Event) { count++ }), 2)
	a.Observe(context.Background(), fastconf.ReloadStarted{})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a.Observe(context.Background(), fastconf.ReloadStarted{})
	if count != 1 || a.Dropped() != 1 {
		t.Fatalf("delivered=%d dropped=%d", count, a.Dropped())
	}
}

func TestJSONLinesCommitFields(t *testing.T) {
	var b bytes.Buffer
	o := observe.JSONLines(&b)
	o.Observe(context.Background(), fastconf.ReloadStarted{})
	o.Observe(context.Background(), fastconf.Committed{Prev: 2, Next: 3, Cause: fastconf.ReloadCause{
		Reason: "manual", At: time.Unix(1, 123456789), Tenant: "tenant", Revisions: map[string]string{"remote": "rev"},
	}})
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"reason": "manual", "at": time.Unix(1, 123456789).Format(time.RFC3339Nano), "generation": float64(3), "previous": float64(2), "tenant": "tenant", "revisions": map[string]any{"remote": "rev"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestAsync_ConcurrentCloseAccountsForEveryEvent(t *testing.T) {
	const producers, perProducer = 4, 50
	for range 20 {
		var delivered atomic.Uint64
		a := observe.Async(observe.Func(func(context.Context, fastconf.Event) { delivered.Add(1) }), 1)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range producers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range perProducer {
					a.Observe(context.Background(), fastconf.EventDropped{Source: "x"})
				}
			}()
		}
		close(start)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if total := delivered.Load() + a.Dropped(); total != producers*perProducer {
			t.Fatalf("delivered+dropped=%d; want %d", total, producers*perProducer)
		}
	}
}

type metricSink struct{ calls []string }

func (s *metricSink) ReloadFinished(ok bool, _ time.Duration) {
	if ok {
		s.calls = append(s.calls, "reload-ok")
	} else {
		s.calls = append(s.calls, "reload-error")
	}
}

func (s *metricSink) StateGeneration(uint64) { s.calls = append(s.calls, "generation") }

func (s *metricSink) LayersTotal(int) { s.calls = append(s.calls, "layers") }

func (s *metricSink) ProviderError(string) { s.calls = append(s.calls, "provider") }

func (s *metricSink) EventDropped(string) { s.calls = append(s.calls, "dropped") }

func (s *metricSink) StageDuration(_ string, _ time.Duration, ok bool) {
	if !ok {
		s.calls = append(s.calls, "stage-error")
	}
}

func TestMetricsEventMapping(t *testing.T) {
	sink := &metricSink{}
	o := observe.Metrics(sink)
	for _, ev := range []fastconf.Event{fastconf.ReloadStarted{}, fastconf.ReloadFinished{},
		fastconf.ReloadFinished{Err: errors.New("failed")}, fastconf.Committed{Next: 3, Layers: 2},
		fastconf.ProviderError{Provider: "remote"}, fastconf.EventDropped{Source: "queue"}, fastconf.StageFinished{Stage: "decode", Err: errors.New("failed")},
	} {
		o.Observe(context.Background(), ev)
	}
	want := []string{"reload-ok", "reload-error", "generation", "layers", "provider", "dropped", "stage-error"}
	if !reflect.DeepEqual(sink.calls, want) {
		t.Fatalf("calls=%v", sink.calls)
	}
}
