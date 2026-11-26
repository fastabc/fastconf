package http

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	serveOK int32 = iota
	serveError
	serveHang
)

// The handler sees transitions, not every failed tick: one error when polls
// start failing, nil on recovery, nothing for a canceled watch.
func TestWatch_ReportsPollFailureTransitions(t *testing.T) {
	var mode atomic.Int32
	hits := make(chan struct{}, 1)
	hung := make(chan struct{}, 1)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch mode.Load() {
		case serveHang:
			signal(hung)
			<-r.Context().Done()
			return
		case serveError:
			w.WriteHeader(500)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", "v1")
			_, _ = w.Write([]byte(`{"value":1}`))
		}
		signal(hits)
	}))
	defer srv.Close()

	reports := make(chan error, 16)
	p, err := New("poll", srv.URL, nil, WithInterval(time.Millisecond),
		WithErrorHandler(func(err error) { reports <- err }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	mode.Store(serveError)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := p.Watch(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := nextReport(t, reports); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("first report = %v; want the 500 error", err)
	}
	// The loop is sequential: each poll is reported before the next request,
	// so three more requests mean at least two more failed polls were handled.
	assertQuiet(t, hits, reports)

	mode.Store(serveOK)
	if err := nextReport(t, reports); err != nil {
		t.Fatalf("recovery report = %v; want nil", err)
	}
	assertQuiet(t, hits, reports)

	// Cancel while a request is in flight: the resulting error is not a
	// polling failure.
	mode.Store(serveHang)
	wait(t, hung)
	cancel()
	for range events {
	}
	if len(reports) != 0 {
		t.Fatalf("canceled watch reported %v", <-reports)
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a request")
	}
}

func nextReport(t *testing.T, reports <-chan error) error {
	t.Helper()
	select {
	case err := <-reports:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
		return nil
	}
}

func assertQuiet(t *testing.T, hits <-chan struct{}, reports <-chan error) {
	t.Helper()
	for range 3 {
		wait(t, hits)
	}
	if len(reports) != 0 {
		t.Fatalf("unexpected report %v", <-reports)
	}
}
