package fastconf

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

type resyncProvider struct {
	name  string
	value atomic.Int64
	load  func(context.Context, int64) error
}

func (p *resyncProvider) Name() string { return p.name }
func (p *resyncProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	v := p.value.Load()
	if p.load != nil {
		if err := p.load(ctx, v); err != nil {
			return contracts.Snapshot{}, err
		}
	}
	return contracts.Snapshot{Map: map[string]any{p.name: v}, Revision: fmt.Sprint(v)}, nil
}

type resyncObserver func(Event)

func (f resyncObserver) Observe(_ context.Context, e Event) { f(e) }

func awaitResync[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for writer/provider barrier")
		var zero T
		return zero
	}
}

// Fill the writer with operations that cannot accidentally publish newer input.
func fillResyncQueue(t *testing.T, m *Manager[map[string]int]) func() {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	m.reloadCh <- reloadRequest{applyFn: func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	awaitResync(t, started)
	for i := 0; i < reloadChanCap; i++ {
		m.reloadCh <- reloadRequest{applyFn: func(context.Context) error { return nil }}
	}
	return unblock
}

func sendResyncEvent(m *Manager[map[string]int], p *resyncProvider) {
	ch := make(chan contracts.Event, 1)
	ch <- contracts.Event{Revision: fmt.Sprint(p.value.Load())}
	close(ch)
	m.consumeProviderEvents(context.Background(), p, ch)
}

func TestProviderOverflowConvergesWithoutAnotherEvent(t *testing.T) {
	p := &resyncProvider{name: "value"}
	p.value.Store(1)
	commits := make(chan Committed, 8)
	drops := 0
	m, err := New[map[string]int](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p),
		WithObserver(resyncObserver(func(e Event) {
			if c, ok := e.(Committed); ok {
				commits <- c
			}
			if _, ok := e.(EventDropped); ok {
				drops++
			}
		})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	awaitResync(t, commits)
	release := fillResyncQueue(t, m)
	p.value.Store(2)
	sendResyncEvent(m, p)
	if drops != 1 {
		t.Fatalf("drops=%d", drops)
	}
	release()
	awaitResync(t, commits)
	if got := (*m.Get())["value"]; got != 2 {
		t.Fatalf("last event lost: value=%d", got)
	}
}

func TestProviderOverflowDuringResyncAndMultipleProviders(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	p := &resyncProvider{name: "a", load: func(ctx context.Context, v int64) error {
		if v == 2 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	q := &resyncProvider{name: "b"}
	p.value.Store(1)
	q.value.Store(1)
	m, err := New[map[string]int](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p, q))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	t.Cleanup(unblock)
	releaseWriter := fillResyncQueue(t, m)
	p.value.Store(2)
	sendResyncEvent(m, p)
	releaseWriter()
	awaitResync(t, started)
	p.value.Store(3)
	q.value.Store(10)
	for i := 0; i < 100; i++ {
		sendResyncEvent(m, p)
		sendResyncEvent(m, q)
	}
	if len(m.resyncCh) != 1 {
		t.Fatal("overflow refresh must stay bounded")
	}
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.enqueue(ctx, reloadRequest{applyFn: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("ordinary request starved: %v", err)
	}
	if got := *m.Get(); got["a"] != 3 || got["b"] != 10 {
		t.Fatalf("did not converge: %v", got)
	}
}

func TestProviderOverflowPauseFailureAndClose(t *testing.T) {
	for _, action := range []string{"pause", "invalid", "close"} {
		t.Run(action, func(t *testing.T) {
			var loads atomic.Int64
			p := &resyncProvider{name: "value", load: func(context.Context, int64) error { loads.Add(1); return nil }}
			p.value.Store(1)
			m, err := New[map[string]int](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p),
				WithValidate(func(c *map[string]int) error {
					if (*c)["value"] < 0 {
						return fmt.Errorf("negative value")
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			before := m.Snapshot()
			release := fillResyncQueue(t, m)
			p.value.Store(-1)
			sendResyncEvent(m, p)
			if action == "close" {
				if err := m.Close(); err != nil {
					t.Fatal(err)
				}
				if m.Snapshot() != before {
					t.Fatal("shutdown published pending input")
				}
				return
			}
			if action == "pause" {
				m.Pause()
				m.Resume()
			}
			release()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := m.enqueue(ctx, reloadRequest{applyFn: func(context.Context) error { return nil }}); err != nil {
				t.Fatal(err)
			}
			if m.Snapshot() != before {
				t.Fatal("invalid or paused update was published")
			}
			wantLoads := int64(1)
			if action == "invalid" {
				wantLoads = 2
				failure := awaitResync(t, m.Errors())
				if failure.Reason != "provider:resync" {
					t.Fatal(failure)
				}
			}
			if loads.Load() != wantLoads {
				t.Fatalf("loads=%d want=%d", loads.Load(), wantLoads)
			}
			p.value.Store(3)
			if err := m.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if (*m.Get())["value"] != 3 {
				t.Fatal("explicit recovery failed")
			}
		})
	}
}
