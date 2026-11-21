package fastconf

import (
	"context"
	"errors"
	"testing"
	"time"
)

// assertNoReloadError fails if Errors holds an entry. Call it after drainWriter,
// which orders every earlier publication before it returns.
func assertNoReloadError[T any](t *testing.T, m *Manager[T]) {
	t.Helper()
	select {
	case re := <-m.Errors():
		t.Fatalf("unexpected Errors entry: reason %q, err %v", re.Reason, re.Err)
	default:
	}
}

// A request whose caller gave up while it was queued never runs. The caller
// gets the context error directly, so Errors must not also carry it.
func TestQueuedCanceledRequestsSkipErrors(t *testing.T) {
	for _, op := range []string{"reload", "plan"} {
		t.Run(op, func(t *testing.T) {
			m, _ := newRollbackFixture(t)
			before := m.Snapshot()
			release := blockWriter(t, m)

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			var err error
			switch op {
			case "reload":
				err = m.Reload(ctx)
			case "plan":
				_, err = m.Plan(ctx)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s with busy writer = %v; want context.DeadlineExceeded", op, err)
			}

			release()
			drainWriter(t, m)
			assertNoReloadError(t, m)
			if got := m.Snapshot(); got != before {
				t.Fatalf("skipped %s published generation %d; want %d", op, got.Generation(), before.Generation())
			}
		})
	}
}

// Failures of requests that did run are still reported after skipped ones.
// Before the fix the expired reloads, queued ahead of the failing request,
// published their own errors first and could evict the real one.
func TestErrorsKeepsFailuresAfterSkippedRequests(t *testing.T) {
	m, _ := newRollbackFixture(t)
	release := blockWriter(t, m)
	for range errorsChanCap + 4 {
		// Created here, so each expires 1 ms from now however late its goroutine runs.
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		go func() {
			defer cancel()
			_ = m.Reload(ctx)
		}()
	}
	failed := make(chan error, 1)
	failure := errors.New("real failure")
	go func() {
		failed <- m.submitAndWait(context.Background(), reloadRequest{
			reason: "real",
			run:    func(context.Context) error { return failure },
		})
	}()
	// Every timed-out request is either still unqueued or queued with an
	// expired context; neither may reach Errors.
	time.Sleep(20 * time.Millisecond)
	release()
	if err := <-failed; !errors.Is(err, failure) {
		t.Fatalf("failing request = %v; want %v", err, failure)
	}
	drainWriter(t, m)
	select {
	case re := <-m.Errors():
		if re.Reason != "real" || !errors.Is(re.Err, failure) {
			t.Fatalf("Errors entry = reason %q err %v; want the real failure", re.Reason, re.Err)
		}
	default:
		t.Fatal("real failure missing from Errors")
	}
	assertNoReloadError(t, m)
}
