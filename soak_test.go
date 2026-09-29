package fastconf_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/observe"
	"github.com/fastabc/fastconf/providers/source"
)

type soakConfig struct {
	Sequence uint64 `json:"sequence"`
	Payload  string `json:"payload"`
}

type soakCounts struct{ commits, previews, reads, canceled uint64 }

// Run separately from other tests so process-wide resource samples are useful:
// FASTCONF_SOAK_DURATION=5m go test -race -run '^TestResourceSoak$' -timeout=10m -v .
func TestResourceSoak(t *testing.T) {
	raw := os.Getenv("FASTCONF_SOAK_DURATION")
	if raw == "" {
		t.Skip("set FASTCONF_SOAK_DURATION to run the resource soak")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < time.Second {
		t.Fatal("FASTCONF_SOAK_DURATION must be a duration of at least 1s")
	}
	sizes := []int{4 << 10, 64 << 10, 1 << 20}
	for _, size := range sizes {
		if _, err := runSoakCycle(200*time.Millisecond, size); err != nil {
			t.Fatalf("warmup %d: %v", size, err)
		}
	}
	baseline, goroutines := soakResources()
	t.Logf("baseline heap=%d objects=%d goroutines=%d", baseline.HeapAlloc, baseline.HeapObjects, goroutines)
	deadline := time.Now().Add(duration)
	var total soakCounts
	for cycle := 0; time.Now().Before(deadline); cycle++ {
		window := min(10*time.Second, time.Until(deadline))
		counts, err := runSoakCycle(window, sizes[cycle%len(sizes)])
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		total.commits += counts.commits
		total.previews += counts.previews
		total.reads += counts.reads
		total.canceled += counts.canceled
		memory, current := soakResources()
		t.Logf("cycle=%d payload=%d commits=%d previews=%d reads=%d canceled=%d heap=%d objects=%d goroutines=%d",
			cycle, sizes[cycle%len(sizes)], counts.commits, counts.previews, counts.reads, counts.canceled,
			memory.HeapAlloc, memory.HeapObjects, current)
		// Tripwires allow runtime caches, but catch accumulating managers or snapshots.
		if memory.HeapAlloc > baseline.HeapAlloc+(16<<20) || current > goroutines+8 {
			t.Fatal("post-close resources exceeded baseline + 16 MiB / 8 goroutines")
		}
	}
	if total.commits == 0 || total.previews == 0 || total.reads == 0 || total.canceled == 0 {
		t.Fatalf("workload did not exercise every operation: %+v", total)
	}
	t.Logf("total commits=%d previews=%d reads=%d canceled=%d", total.commits, total.previews, total.reads, total.canceled)
}

func soakResources() (runtime.MemStats, int) {
	// Two collections also expire sync.Pool victim caches between manager cycles.
	runtime.GC()
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return memory, runtime.NumGoroutine()
}

func runSoakCycle(duration time.Duration, size int) (soakCounts, error) {
	var counts soakCounts
	doc, err := json.Marshal(soakConfig{Payload: strings.Repeat("x", size)})
	if err != nil {
		return counts, err
	}
	gate := make(chan struct{})
	reporter := observe.Async(observe.Func(func(_ context.Context, e fastconf.Event) {
		if _, ok := e.(fastconf.Committed); ok {
			<-gate
		}
	}), 2)
	m, err := fastconf.New[soakConfig](context.Background(),
		// Exercise decoded file-layer cache retention alongside source loading.
		fastconf.WithFS(fstest.MapFS{"conf.d/base/config.json": &fstest.MapFile{Data: doc}}),
		fastconf.WithProvider(source.NewBytes("soak", "json", doc)),
		fastconf.WithHistory(4),
		fastconf.WithObserver(reporter),
		fastconf.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		return counts, err
	}
	var release sync.Once
	defer func() {
		release.Do(func() { close(gate) })
		_ = m.Close()
		_ = reporter.Close()
	}()
	initial := m.Snapshot()
	var callbacks atomic.Uint64
	cancelSub := fastconf.Subscribe(m, func(c *soakConfig) *uint64 { return &c.Sequence },
		func(_, _ *uint64) { callbacks.Add(1) })
	defer cancelSub()
	work, stop := context.WithTimeout(context.Background(), duration)
	defer stop()
	operation, cancelOperation := context.WithTimeout(context.Background(), duration+5*time.Second)
	defer cancelOperation()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	launch := func(name string, action func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work.Err() == nil {
				if err := action(); err != nil {
					failures <- fmt.Errorf("%s: %w", name, err)
					stop()
					return
				}
			}
		}()
	}
	launch("reload", func() error {
		if err := m.Reload(operation, fastconf.WithOverride(map[string]any{"sequence": counts.commits + 1})); err != nil {
			return err
		}
		counts.commits++
		return nil
	})
	launch("plan", func() error {
		plan, err := m.Plan(operation)
		if err != nil {
			return err
		}
		if plan.Proposed.Hash() != initial.Hash() || plan.Proposed.Value().Sequence != 0 {
			return errors.New("preview does not describe the base source")
		}
		counts.previews++
		return nil
	})
	launch("read", func() error {
		snapshot := m.Snapshot()
		encoded, err := json.Marshal(snapshot.Value())
		if err != nil {
			return err
		}
		if snapshot.Hash() != sha256.Sum256(encoded) || len(snapshot.Value().Payload) != size {
			return errors.New("snapshot value and hash disagree")
		}
		counts.reads++
		return nil
	})
	launch("cancel", func() error {
		if err := m.Reload(canceled); !errors.Is(err, context.Canceled) {
			return fmt.Errorf("canceled Reload returned %v", err)
		}
		if _, err := m.Plan(canceled); !errors.Is(err, context.Canceled) {
			return fmt.Errorf("canceled Plan returned %v", err)
		}
		counts.canceled += 2
		return nil
	})
	wg.Wait()
	close(failures)
	for err := range failures {
		return counts, err
	}
	release.Do(func() { close(gate) })
	if err := m.Shutdown(operation); err != nil {
		return counts, err
	}
	for failure := range m.Errors() {
		if !errors.Is(failure.Err, context.Canceled) {
			return counts, fmt.Errorf("unexpected asynchronous error: %w", failure.Err)
		}
	}
	if got := m.Snapshot(); got.Generation() != initial.Generation()+counts.commits || got.Value().Sequence != counts.commits {
		return counts, errors.New("commits, final value, and generation disagree")
	}
	if initial.Value().Sequence != 0 || callbacks.Load() != counts.commits || len(m.History().List()) > 4 {
		return counts, errors.New("snapshot immutability, callbacks, or history bound violated")
	}
	if counts.commits > 3 && reporter.Dropped() == 0 {
		return counts, fmt.Errorf("async observer dropped nothing across %d commits", counts.commits)
	}
	return counts, nil
}
