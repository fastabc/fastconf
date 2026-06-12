package fastconf

// B5: Plan().Run() must execute on the single-writer reload goroutine so
// user hooks are never invoked from two goroutines at once. These tests
// pin that invariant (race detector) and the documented behavior that a
// failing Plan is published on Errors().

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

type planCfg struct {
	Port int `json:"port" yaml:"port"`
}

// countingTransformer increments an UNSYNCHRONIZED counter. If Plan and
// reload ever run it concurrently, the race detector fires.
type countingTransformer struct{ count int }

func (c *countingTransformer) Name() string { return "counter" }
func (c *countingTransformer) Transform(map[string]any) error {
	c.count++
	return nil
}

func TestPlan_SerializesWithReload(t *testing.T) {
	tr := &countingTransformer{}
	mgr, err := New[planCfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("port: 8080\n")), nil),
		WithTransformers(tr),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = mgr.Plan().Run(context.Background()) }()
		go func() { defer wg.Done(); _ = mgr.Reload(context.Background()) }()
	}
	wg.Wait()
}

// toggleTransformer fails only once armed, so New() succeeds first.
type toggleTransformer struct{ fail atomic.Bool }

func (t *toggleTransformer) Name() string { return "toggle" }
func (t *toggleTransformer) Transform(map[string]any) error {
	if t.fail.Load() {
		return errors.New("toggle: armed")
	}
	return nil
}

func TestPlan_FailurePublishedToErrors(t *testing.T) {
	tr := &toggleTransformer{}
	mgr, err := New[planCfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("port: 8080\n")), nil),
		WithTransformers(tr),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	tr.fail.Store(true)
	if _, err := mgr.Plan().Run(context.Background()); err == nil {
		t.Fatal("expected Plan to fail with armed transformer")
	}
	select {
	case re := <-mgr.Errors():
		if re.Err == nil {
			t.Fatal("published ReloadError carries no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Plan failure was not published on Errors()")
	}
}
