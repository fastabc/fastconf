package fastconf_test

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

	// Replicate the old "callback after N consecutive failures" pattern
	// purely from the consumer side using Errors().
	fail.Store(true)
	for range 3 {
		_ = mgr.Reload(context.Background())
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
		if re.When.IsZero() {
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
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: ok\n")},
	}
	var fail atomic.Bool
	mgr, err := fastconf.New[errorStreamConfig](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithValidate(func(_ *errorStreamConfig) error {
			if fail.Load() {
				return errors.New("always-fail")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	fail.Store(true)
	const attempts = 20
	for i := range attempts {
		reason := fmt.Sprintf("manual-%02d", i)
		_ = mgr.Reload(context.Background(), fastconf.WithReason(reason))
	}

	got := drain(t, mgr.Errors(), 16, time.Second)
	if len(got) != 16 {
		t.Fatalf("want 16 retained errors, got %d", len(got))
	}
	if got[0].Reason != "manual-04" {
		t.Fatalf("oldest retained reason=%q want manual-04", got[0].Reason)
	}
	if got[len(got)-1].Reason != "manual-19" {
		t.Fatalf("newest retained reason=%q want manual-19", got[len(got)-1].Reason)
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
