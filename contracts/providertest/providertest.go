// Package providertest contains reusable conformance checks for packages implementing
// contracts.Provider.
package providertest

import (
	"context"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// TB is the subset of testing.TB used by this package.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AssertProviderBasics verifies the stable invariants every Provider should satisfy without
// requiring a live watch event source.
func AssertProviderBasics(t TB, p contracts.Provider) {
	t.Helper()
	if p == nil {
		t.Fatalf("provider is nil")
	}
	name := p.Name()
	if name == "" {
		t.Fatalf("Provider.Name() is empty")
	}
	if got := p.Name(); got != name {
		t.Fatalf("Provider.Name() is unstable: got %q after %q", got, name)
	}
	priority := contracts.Describe(p).Priority
	if got := contracts.Describe(p).Priority; got != priority {
		t.Fatalf("Describe().Priority is unstable: got %d after %d", got, priority)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Provider.Load(): %v", err)
	}
}

// AssertWatchClosesOnCancel verifies that Watch returns a channel that observes context
// cancellation. Static providers may return nil.
func AssertWatchClosesOnCancel(t TB, p contracts.Provider, timeout time.Duration) {
	t.Helper()
	if timeout <= 0 {
		timeout = time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Watch(ctx, "")
	if err != nil {
		cancel()
		t.Fatalf("Provider.Watch(): %v", err)
	}
	cancel()
	if ch == nil {
		return
	}
	waitClosed(t, ch, timeout, "Provider.Watch")
}

func waitClosed(t TB, ch <-chan contracts.Event, timeout time.Duration, label string) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatalf("%s channel did not close within %s after cancel", label, timeout)
		}
	}
}
