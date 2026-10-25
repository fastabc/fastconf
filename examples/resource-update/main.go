// Command resource-update demonstrates bounded, latest-state resource updates.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

type DBConfig struct {
	Endpoint string `json:"endpoint" fc:"required"`
}
type Config struct {
	Database DBConfig `json:"database"`
}

// resource models an owned connection. Replace build with a real connector
// that honors ctx; the ownership and shutdown rules remain the same.
type resource struct {
	endpoint string
	closed   bool
}

func (r *resource) close() { r.closed = true }

type slot struct {
	mu      sync.RWMutex
	current *resource
}

// Requests hold the read lock for their entire use of the resource. A swap
// waits for these users before closing the old resource.
func (s *slot) use(fn func(*resource)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(s.current)
}

func (s *slot) replace(next *resource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.current
	s.current = next
	if previous != nil {
		previous.close()
	}
}

type result struct {
	desired, applied uint64
	err              error
}
type builder func(context.Context, DBConfig) (*resource, error)

func notify(updates chan<- struct{}) {
	select {
	case updates <- struct{}{}:
	default:
	}
}

func updateResources(ctx context.Context, mgr *fastconf.Manager[Config], s *slot,
	updates chan struct{}, results chan<- result, build builder) {
	var applied uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
		}
		state := mgr.Snapshot()
		cfg := state.Value().Database // String-only value copy; deep-copy collections if added.
		buildCtx, cancel := context.WithTimeout(ctx, time.Second)
		next, err := build(buildCtx, cfg)
		cancel()
		if ctx.Err() != nil {
			if next != nil {
				next.close()
			}
			return
		}
		if err == nil && mgr.Snapshot().Generation() != state.Generation() {
			next.close()
			notify(updates)
			continue
		}
		if err == nil {
			s.replace(next)
			applied = state.Generation()
		}
		// Telemetry is bounded and may be dropped; resource convergence uses
		// updates and the latest snapshot, not this reporting channel.
		select {
		case results <- result{state.Generation(), applied, err}:
		default:
		}
	}
}

func run(out io.Writer) (err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start with a valid configuration before constructing dependent resources.
	mgr, err := fastconf.New[Config](ctx, fastconf.WithFS(fstest.MapFS{
		"conf.d/base/app.yaml": {Data: []byte("database: {endpoint: A}")},
	}), fastconf.WithUnknownFields(fastconf.UnknownError))
	if err != nil {
		return err
	}

	// Callbacks only signal; one worker builds and swaps resources.
	var resources slot
	updates := make(chan struct{}, 1)
	results := make(chan result, 1)
	done := make(chan struct{})
	unsubscribe := fastconf.Subscribe(mgr, func(c *Config) *DBConfig { return &c.Database },
		func(_, _ *DBConfig) { notify(updates) })
	go func() {
		defer close(done)
		updateResources(ctx, mgr, &resources, updates, results, func(ctx context.Context, cfg DBConfig) (*resource, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if cfg.Endpoint == "B" {
				return nil, fmt.Errorf("connection to B failed")
			}
			return &resource{endpoint: cfg.Endpoint}, nil
		})
	}()

	// Stop producers, join the worker, then release the final active resource.
	defer func() {
		closeErr := mgr.Close() // No more callbacks after this returns.
		unsubscribe()
		cancel()
		<-done
		resources.replace(nil) // Wait for requests, then close the active resource.
		if err == nil {
			err = closeErr
		}
	}()

	// A failed connection to B keeps A usable; the next update converges to C.
	notify(updates) // Subscribe does not send initial state.
	for _, endpoint := range []string{"A", "B", "C"} {
		if endpoint != "A" {
			if err := mgr.Reload(ctx, fastconf.WithOverride(map[string]any{
				"database": map[string]any{"endpoint": endpoint},
			})); err != nil {
				return err
			}
		}
		var r result
		select {
		case r = <-results:
		case <-time.After(3 * time.Second):
			return fmt.Errorf("resource update timed out")
		}
		var active string
		resources.use(func(current *resource) {
			if current != nil {
				active = current.endpoint
			}
		})
		if _, err := fmt.Fprintf(out, "desired=%d applied=%d active=%s failed=%t\n", r.desired, r.applied, active, r.err != nil); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
