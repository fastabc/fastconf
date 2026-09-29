package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

type errorStreamConfig struct {
	Name string `json:"name"`
}

func TestErrors_NoEventsOnSuccess(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: ok\n")},
	}
	mgr, err := fastconf.New[errorStreamConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	for range 3 {
		if err := mgr.Reload(context.Background()); err != nil {
			t.Fatalf("unexpected reload error: %v", err)
		}
	}
	// No errors should have landed on the channel.
	select {
	case re := <-mgr.Errors():
		t.Fatalf("unexpected ReloadError on success path: %+v", re)
	default:
	}
	if mgr.Get().Name != "ok" {
		t.Errorf("Get().Name = %q", mgr.Get().Name)
	}
}

func TestErrors_ChannelDeliversPerFailure(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: ok\n")},
	}
	var fail atomic.Bool
	mgr, err := fastconf.New[errorStreamConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithValidate(func(_ *errorStreamConfig) error {
			if fail.Load() {
				return errors.New("flip-flopped")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	// Every failed attempt produces its own error event.
	fail.Store(true)
	for range 3 {
		if err := mgr.Reload(context.Background()); !errors.Is(err, fastconf.ErrInvalid) {
			t.Fatalf("Reload = %v; want ErrInvalid", err)
		}
	}

	got := drain(t, mgr.Errors(), 3, time.Second)
	if len(got) != 3 {
		t.Fatalf("want 3 errors, got %d (%+v)", len(got), got)
	}
	for _, re := range got {
		if re.Err == nil {
			t.Errorf("nil err in event: %+v", re)
		}
		if re.Reason != "manual" {
			t.Errorf("reason: %q", re.Reason)
		}
		if re.At.IsZero() {
			t.Errorf("zero timestamp")
		}
	}

	// A successful reload emits nothing.
	fail.Store(false)
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatalf("expected successful reload, got %v", err)
	}
	select {
	case re := <-mgr.Errors():
		t.Errorf("unexpected event after successful reload: %+v", re)
	default:
	}
}

func TestErrors_ChannelDropsOldestWhenFull(t *testing.T) {
	var fail atomic.Bool
	mgr, err := fastconf.New[errorStreamConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("name: ok\n")}}), fastconf.WithValidate(func(*errorStreamConfig) error {
		if fail.Load() {
			return errors.New("invalid")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	retained := cap(mgr.Errors())
	if retained == 0 {
		t.Fatal("error stream must buffer failures without blocking reload")
	}
	fail.Store(true)
	const overflow = 4
	for i := range retained + overflow {
		if err := mgr.Reload(context.Background(), fastconf.WithReason(fmt.Sprintf("manual-%02d", i))); !errors.Is(err, fastconf.ErrInvalid) {
			t.Fatalf("Reload=%v; want ErrInvalid", err)
		}
	}
	got := drain(t, mgr.Errors(), retained, time.Second)
	if len(got) != retained {
		t.Fatalf("retained=%d; want %d", len(got), retained)
	}
	for i, event := range got {
		if want := fmt.Sprintf("manual-%02d", i+overflow); event.Reason != want {
			t.Fatalf("error[%d].Reason=%q; want %q", i, event.Reason, want)
		}
	}
}

func TestErrors_CloseClosesChannel(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: ok\n")},
	}
	mgr, err := fastconf.New[errorStreamConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	errs := mgr.Errors()
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-errs; ok {
		t.Fatal("Errors channel should be closed after Close")
	}
}

// TestReloadOptionErrorPublishedOnce verifies: an invalid Reload option
// returns its error synchronously and publishes it once on Errors(); after
// Close the same call reports ErrClosed.
func TestReloadOptionErrorPublishedOnce(t *testing.T) {
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	bad := fastconf.WithOverride(map[string]any{"ch": make(chan int)})
	if err := mgr.Reload(context.Background(), bad); !errors.Is(err, fastconf.ErrDecode) {
		t.Fatalf("Reload = %v; want ErrDecode", err)
	}
	select {
	case re := <-mgr.Errors():
		if !errors.Is(re.Err, fastconf.ErrDecode) || re.Reason != "override" {
			t.Fatalf("published %+v; want ErrDecode with reason override", re)
		}
	case <-time.After(time.Second):
		t.Fatal("option error not published on Errors()")
	}
	select {
	case extra := <-mgr.Errors():
		t.Fatalf("duplicate error event: %+v", extra)
	default:
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(context.Background(), bad); !errors.Is(err, fastconf.ErrClosed) {
		t.Fatalf("Reload after Close = %v; want ErrClosed", err)
	}
}

// drain pulls up to n events off ch within timeout; returns however many
// it managed to collect.
func drain(t *testing.T, ch <-chan fastconf.ReloadError, n int, timeout time.Duration) []fastconf.ReloadError {
	t.Helper()
	out := make([]fastconf.ReloadError, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(out) < n {
		select {
		case re, ok := <-ch:
			if !ok {
				t.Fatalf("Errors channel closed after %d of %d events", len(out), n)
			}
			out = append(out, re)
		case <-deadline.C:
			return out
		}
	}
	return out
}
