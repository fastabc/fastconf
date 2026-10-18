package main

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

func TestFailedBuildKeepsResource(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "desired=1 applied=1 active=A failed=false\ndesired=2 applied=1 active=A failed=true\ndesired=3 applied=3 active=C failed=false\n"
	if out.String() != want {
		t.Fatalf("got %s", &out)
	}
}

func TestSlowBuildCoalescesToLatestAndDiscardsStale(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m, err := fastconf.New[Config](ctx, fastconf.WithFS(fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("database: {endpoint: A}")}}))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var s slot
	updates, results := make(chan struct{}, 1), make(chan result, 1)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stale := &resource{endpoint: "A"}
	var builds atomic.Int64
	go func() {
		defer close(done)
		updateResources(ctx, m, &s, updates, results, func(ctx context.Context, cfg DBConfig) (*resource, error) {
			builds.Add(1)
			if cfg.Endpoint == "A" {
				close(started)
				select {
				case <-release:
					return stale, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &resource{endpoint: cfg.Endpoint}, nil
		})
	}()
	unsubscribe := fastconf.Subscribe(m, func(c *Config) *DBConfig { return &c.Database }, func(_, _ *DBConfig) { notify(updates) })
	defer func() { _ = m.Close(); unsubscribe(); cancel(); <-done; s.replace(nil) }()
	notify(updates)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no build")
	}
	for _, endpoint := range []string{"B", "C"} {
		if err := m.Reload(ctx, fastconf.WithOverride(map[string]any{"database": map[string]any{"endpoint": endpoint}})); err != nil {
			t.Fatal(err)
		}
	}
	if len(updates) != 1 {
		t.Fatal("notifications were not coalesced")
	}
	close(release)
	select {
	case r := <-results:
		if r.applied != 3 || r.err != nil || !stale.closed {
			t.Fatalf("result=%+v staleClosed=%t", r, stale.closed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no final resource")
	}
	if builds.Load() != 2 {
		t.Fatalf("build count=%d", builds.Load())
	}
	s.use(func(r *resource) {
		if r.endpoint != "C" || r.closed {
			t.Fatal("wrong active resource")
		}
	})
}

func TestResourceSwapWaitsForRequests(t *testing.T) {
	old := &resource{endpoint: "A"}
	s := &slot{current: old}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go s.use(func(r *resource) {
		close(entered)
		<-release
		if r.closed {
			t.Error("closed during request")
		}
	})
	<-entered
	go func() { s.replace(&resource{endpoint: "C"}); close(done) }()
	// The request holds RLock, so replace cannot close old before release.
	select {
	case <-done:
		t.Fatal("swap bypassed an active request")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("swap did not finish")
	}
	if !old.closed {
		t.Fatal("old resource leaked")
	}
	s.replace(nil)
}
