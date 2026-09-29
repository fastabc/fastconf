package fastconf

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

func TestLoadRunsOneShotWithoutConfigDirectory(t *testing.T) {
	s, err := Load[struct {
		Port int `json:"port"`
	}](context.Background(), WithProvider(source.NewBytes("inline", "json", []byte(`{"port": 8080}`))))
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Value().Port != 8080 {
		t.Fatalf("snapshot = %#v", s)
	}
}

// TestLoadHonorsFileDiscoveryOptions verifies that Load resolves the same option
// set as New, including WithFS / WithDir / WithProfile.
func TestLoadHonorsFileDiscoveryOptions(t *testing.T) {
	fs := fstest.MapFS{
		"cfg/base/00.yaml":          &fstest.MapFile{Data: []byte("port: 1\n")},
		"cfg/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("port: 2\n")},
	}
	s, err := Load[struct {
		Port int `json:"port"`
	}](context.Background(), WithFS(fs), WithDir("cfg"), WithProfile(Profile{Names: []string{"prod"}}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Value().Port != 2 {
		t.Fatalf("port = %d; want 2 from the prod overlay", s.Value().Port)
	}
}

func TestLoadReturnsNoSnapshotOnFailure(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		data string
		want error
	}{
		{"source-parse", context.Background(), `{`, ErrProvider},
		{"decode", context.Background(), `{"port":{}}`, ErrDecode},
		{"validation", context.Background(), `{"port":0}`, ErrInvalid},
		{"canceled", canceled, `{"port":8080}`, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Load[struct {
				Port int `json:"port" fc:"min=1"`
			}](tc.ctx, WithProvider(source.NewBytes("inline", "json", []byte(tc.data))))
			if s != nil || !errors.Is(err, tc.want) {
				t.Fatalf("Load = %v, %v; want nil, %v", s, err, tc.want)
			}
		})
	}
}

func TestShutdownRetriesKeepGoroutinesBounded(t *testing.T) {
	m := newRuntimeManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = m.Close()
	})
	cancelSub := Subscribe(m, func(c *runtimeConfig) *int { return &c.A }, func(_, _ *int) {
		close(entered)
		<-release
	})
	defer cancelSub()
	reloaded := make(chan error, 1)
	go func() {
		reloaded <- m.Reload(context.Background(), WithOverride(map[string]any{"a": 2}))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber did not start")
	}
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown with blocked subscriber = %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	for i := 0; i < 128; i++ {
		if err := m.Shutdown(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown retry %d = %v", i, err)
		}
	}
	// Allow unrelated runtime workers, but not a new worker for every retry.
	if growth := runtime.NumGoroutine() - before; growth > 8 {
		t.Fatalf("128 shutdown retries added %d goroutines", growth)
	}
	select {
	case _, ok := <-m.Errors():
		if !ok {
			t.Fatal("Errors closed before the subscriber finished")
		}
	default:
	}
	releaseOnce.Do(func() { close(release) })
	finish, cancelFinish := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFinish()
	if err := m.Shutdown(finish); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-m.Errors(); ok {
		t.Fatal("Errors remains open after shutdown completed")
	}
	select {
	case err := <-reloaded:
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("in-flight Reload = %v", err)
		}
	case <-finish.Done():
		t.Fatal("in-flight Reload did not finish")
	}
}

func TestShutdownCompletedIgnoresCanceledContext(t *testing.T) {
	m := newRuntimeManager(t)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 32; i++ {
		if err := m.Shutdown(ctx); err != nil {
			t.Fatalf("completed Shutdown retry %d = %v", i, err)
		}
	}
}
