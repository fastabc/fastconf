package fastconf

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
	"github.com/fastabc/fastconf/internal/testutil"
)

func TestWatchRetryDelay(t *testing.T) {
	b := providerutil.Backoff{Min: providerWatchMinDelay, Max: providerWatchMaxDelay}
	short := time.Millisecond
	for i, want := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second} {
		if got := watchRetryDelay(&b, short); got != want {
			t.Fatalf("short-lived stream #%d: delay %v; want %v", i, got, want)
		}
	}
	for range 10 {
		watchRetryDelay(&b, short)
	}
	if got := watchRetryDelay(&b, short); got != providerWatchMaxDelay {
		t.Fatalf("repeated short-lived streams: delay %v; want cap %v", got, providerWatchMaxDelay)
	}
	if got := watchRetryDelay(&b, providerWatchHealthy); got != providerWatchMinDelay {
		t.Fatalf("healthy stream end: delay %v; want reset to %v", got, providerWatchMinDelay)
	}
	if got := watchRetryDelay(&b, short); got != 2*providerWatchMinDelay {
		t.Fatalf("short-lived stream after reset: delay %v; want %v", got, 2*providerWatchMinDelay)
	}
}

// closingWatcher returns an already-closed channel from every Watch call.
type closingWatcher struct {
	mu    sync.Mutex
	calls []time.Time
}

func (*closingWatcher) Name() string { return "closing" }

func (*closingWatcher) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"ok": true}}, nil
}

func (w *closingWatcher) Watch(context.Context, string) (<-chan contracts.Event, error) {
	w.mu.Lock()
	w.calls = append(w.calls, time.Now())
	w.mu.Unlock()
	ch := make(chan contracts.Event)
	close(ch)
	return ch, nil
}

func (w *closingWatcher) times() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.calls)
}

type watchClosedCounter struct{ n atomic.Int64 }

func (c *watchClosedCounter) Observe(_ context.Context, e Event) {
	if pe, ok := e.(ProviderError); ok && errors.Is(pe.Err, errWatchClosed) {
		c.n.Add(1)
	}
}

// A Watch that closes its channel immediately must resubscribe with growing
// delays and report each early close. Sleeps never end early, so the lower
// bounds on the gaps are deterministic; jitter only lengthens them.
func TestProviderWatchEarlyCloseBacksOff(t *testing.T) {
	oldMin, oldMax, oldHealthy := providerWatchMinDelay, providerWatchMaxDelay, providerWatchHealthy
	providerWatchMinDelay, providerWatchMaxDelay, providerWatchHealthy = 2*time.Millisecond, 32*time.Millisecond, time.Hour
	t.Cleanup(func() {
		providerWatchMinDelay, providerWatchMaxDelay, providerWatchHealthy = oldMin, oldMax, oldHealthy
	})

	p := &closingWatcher{}
	counter := &watchClosedCounter{}
	m, err := New[map[string]any](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{}`)}}),
		WithProvider(p),
		WithObserver(counter),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Registered after the timing restore, so it runs first and the watcher
	// goroutine has exited before the variables change back.
	t.Cleanup(func() { _ = m.Close() })

	const n = 7
	testutil.WaitFor(t, func() bool { return len(p.times()) >= n }, 5*time.Second, "provider resubscribes")
	calls := p.times()
	want := providerWatchMinDelay
	for i := 1; i < n; i++ {
		if gap := calls[i].Sub(calls[i-1]); gap < want {
			t.Fatalf("resubscribe #%d after %v; want at least %v", i, gap, want)
		}
		want = min(2*want, providerWatchMaxDelay)
	}
	if got := counter.n.Load(); got < n-1 {
		t.Fatalf("early-close ProviderError events = %d; want at least %d", got, n-1)
	}
}
