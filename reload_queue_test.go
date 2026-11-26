package fastconf

import (
	"context"
	"errors"
	"slices"
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
	for _, op := range []string{"reload", "plan", "rollback"} {
		t.Run(op, func(t *testing.T) {
			m, target := newRollbackFixture(t)
			before, history := m.Snapshot(), m.History().List()
			release := blockWriter(t, m)

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				switch op {
				case "reload":
					result <- m.Reload(ctx)
				case "plan":
					_, err := m.Plan(ctx)
					result <- err
				case "rollback":
					result <- m.History().RollbackContext(ctx, target)
				}
			}()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("%s with busy writer = %v; want context.DeadlineExceeded", op, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s did not return by its deadline", op)
			}

			release()
			drainWriter(t, m)
			assertNoReloadError(t, m)
			if got := m.Snapshot(); got != before {
				t.Fatalf("skipped %s published generation %d; want %d", op, got.Generation(), before.Generation())
			}
			if got := m.History().List(); !slices.Equal(got, history) {
				t.Fatalf("skipped %s changed history", op)
			}
		})
	}
}

// Skipped requests cannot evict a failure already in Errors.
func TestErrorsKeepsFailuresAfterSkippedRequests(t *testing.T) {
	m, _ := newRollbackFixture(t)
	failure := errors.New("real failure")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.submitAndWait(ctx, reloadRequest{
		reason: "real",
		run:    func(context.Context) error { return failure },
	}); !errors.Is(err, failure) {
		t.Fatalf("failing request = %v; want %v", err, failure)
	}
	// Filling the queue only proves no eviction while it can hold more
	// skipped requests than Errors can buffer.
	if cap(m.reloadCh) < cap(m.errorsCh) {
		t.Fatalf("reload queue cap %d < Errors cap %d; the test cannot overflow Errors",
			cap(m.reloadCh), cap(m.errorsCh))
	}
	release := blockWriter(t, m)
	expired, expire := context.WithCancel(context.Background())
	for range cap(m.reloadCh) {
		m.reloadCh <- reloadRequest{ctx: expired, trigger: TriggerManual}
	}
	// Cancel the enqueued requests before allowing the writer to reach them.
	expire()
	release()
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
