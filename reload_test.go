package fastconf

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
)

type errProvider struct {
	err  error
	fail atomic.Bool
}

func (e *errProvider) Name() string { return "err-provider" }

func (e *errProvider) Describe() contracts.ProviderInfo { return contracts.ProviderInfo{Priority: 0} }

func (e *errProvider) Load(context.Context) (contracts.Snapshot, error) {
	if e.fail.Load() {
		return contracts.Snapshot{}, e.err
	}
	return contracts.Snapshot{Map: map[string]any{"port": 8080}}, nil
}

func TestReload_ProviderFailureSkipsCommit(t *testing.T) {
	boom := errors.New("provider unavailable")
	p := &errProvider{err: boom}
	m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	before := m.Snapshot()
	p.fail.Store(true)
	if err := m.Reload(context.Background()); !errors.Is(err, boom) || !errors.Is(err, ErrProvider) {
		t.Fatalf("Reload = %v; want provider cause and ErrProvider", err)
	}
	if m.Snapshot() != before {
		t.Fatal("provider failure replaced committed snapshot")
	}
}

func TestRejectPendingReloads(t *testing.T) {
	m := &Manager[struct{}]{reloadCh: make(chan reloadRequest, 3)}
	first, second := make(chan error, 1), make(chan error, 1)
	m.reloadCh <- reloadRequest{doneCh: first}
	m.reloadCh <- reloadRequest{} // Watch events do not have a waiting caller.
	m.reloadCh <- reloadRequest{doneCh: second}
	m.rejectPendingReloads()
	if len(m.reloadCh) != 0 {
		t.Fatal("pending queue was not drained")
	}
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("queued error=%v; want ErrClosed", err)
			}
		default:
			t.Fatal("queued caller was not released")
		}
	}
	m.rejectPendingReloads() // Draining an empty queue is safe.
}

func TestRunReloadRequestSkipsCanceledWork(t *testing.T) {
	for _, lifetime := range []bool{false, true} {
		m, err := newManager[struct{}](context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if lifetime {
			m.lifetimeCancel()
		} else {
			cancel()
		}
		called := false
		done := make(chan error, 1)
		m.runReloadRequest(reloadRequest{ctx: ctx, doneCh: done, applyFn: func(context.Context) error {
			called = true
			return nil
		}})
		cancel()
		m.lifetimeCancel()
		if err := <-done; called || !errors.Is(err, context.Canceled) {
			t.Fatalf("lifetime=%v: called=%v, err=%v; want skipped work and cancellation", lifetime, called, err)
		}
	}
}
