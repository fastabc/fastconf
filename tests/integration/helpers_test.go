package integration_test

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/testutil"
	"github.com/fastabc/fastconf/observe"
)

type dbCfg struct {
	DSN  string `yaml:"dsn" json:"dsn"`
	Pool int    `yaml:"pool" json:"pool"`
}

type appCfg struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Database dbCfg    `yaml:"database" json:"database"`
	Features []string `yaml:"features" json:"features"`
}

func newFS(extra map[string]string) fstest.MapFS {
	fs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
features: [a, b]
`)},
		"conf.d/base/20-database.yaml": &fstest.MapFile{Data: []byte(`
database:
  dsn: "postgres://base"
  pool: 10
`)},
	}
	for k, v := range extra {
		fs[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return fs
}

func writeFile(t *testing.T, path, content string) { t.Helper(); testutil.WriteFile(t, path, content) }

func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	testutil.WaitFor(t, cond, 3*time.Second, message)
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before the expected result")
		}
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a result")
		var zero T
		return zero
	}
}

type recObserver struct {
	mu     sync.Mutex
	events []fastconf.Event
}

func (r *recObserver) Observe(_ context.Context, e fastconf.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recObserver) snapshot() []fastconf.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fastconf.Event(nil), r.events...)
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type obsCfg struct {
	Name string `json:"name"`
}

func observerManager(t *testing.T, opts ...fastconf.Option) *fastconf.Manager[obsCfg] {
	t.Helper()
	opts = append(opts, fastconf.WithFS(fstest.MapFS{"conf.d/base/a.yaml": &fstest.MapFile{Data: []byte("name: initial\n")}}))
	m, err := fastconf.New[obsCfg](context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// onCommit returns an observer that runs fn for Committed events after the
// initial load.
func onCommit(fn func(ctx context.Context, c fastconf.Committed)) fastconf.Observer {
	return observe.Func(func(ctx context.Context, e fastconf.Event) {
		if c, ok := e.(fastconf.Committed); ok && c.Cause.Reason != "initial" {
			fn(ctx, c)
		}
	})
}

type cfgAudit struct {
	Name string `yaml:"name"`
}

// recordingMetrics counts the events a metrics exporter consumes.
type recordingMetrics struct {
	started  atomic.Int64
	okCount  atomic.Int64
	errCount atomic.Int64
	gen      atomic.Uint64
	layers   atomic.Int64
}

func (r *recordingMetrics) Observe(_ context.Context, e fastconf.Event) {
	switch ev := e.(type) {
	case fastconf.ReloadStarted:
		r.started.Add(1)
	case fastconf.ReloadFinished:
		if ev.Err == nil {
			r.okCount.Add(1)
		} else {
			r.errCount.Add(1)
		}
	case fastconf.Committed:
		r.gen.Store(ev.Next)
		r.layers.Store(int64(ev.Layers))
	}
}

type gateProvider struct {
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (p *gateProvider) Name() string { return "gate" }

func (p *gateProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 100}
}

func (p *gateProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	if !p.block.Load() {
		return contracts.Snapshot{Map: map[string]any{}}, nil
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return contracts.Snapshot{Map: map[string]any{}}, nil
	case <-ctx.Done():
		return contracts.Snapshot{}, ctx.Err()
	}
}
