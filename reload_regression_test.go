package fastconf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/codec"
)

func TestReload_EqualValueRefreshesSecretMetadata(t *testing.T) {
	fs := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("token: canary\n")}}
	m, err := New[map[string]string](context.Background(), WithFS(fs), WithSecretResolvers(PrefixSecretResolver("enc", "enc:", func(_ context.Context, body string) (string, error) { return body, nil })))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("token: enc:canary\n")}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Map()["token"]; got != "***REDACTED***" {
		t.Fatalf("resolved secret remains unmasked after equal-value reload: %v", got)
	}
}

func TestFileWatch_InitialReadGap(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "base"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "base", "a.yaml")
	if err := os.WriteFile(path, []byte("value: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var once atomic.Bool
	m, err := New[map[string]string](context.Background(), WithDir(dir), WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}), WithValidate(func(_ *map[string]string) error {
		if once.CompareAndSwap(false, true) {
			return os.WriteFile(path, []byte("value: new\n"), 0600)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if (*m.Get())["value"] == "new" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watch lost update between initial read and watch registration: got %s", (*m.Get())["value"])
}

func TestInputFingerprint_CacheCapacityBoundary(t *testing.T) {
	for _, n := range []int{512, 513} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var calls atomic.Int64
			name := fmt.Sprintf("capacity-boundary-%d", n)
			codec.Register(name, countingLayerCodec{&calls, ""})
			codec.RegisterExt(name, name)
			fs := fstest.MapFS{}
			for i := 0; i < n; i++ {
				fs[fmt.Sprintf("conf.d/base/%04d.%s", i, name)] = &fstest.MapFile{Data: []byte("value")}
			}
			m, err := New[map[string]any](context.Background(), WithFS(fs))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			calls.Store(0)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("unchanged reload of %d layers decoded %d files", n, got)
			}
		})
	}
}

func TestFileWatch_InactiveOverlayMetadata(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"base/a.yaml": "value: base\n", "overlays/candidate/a.yaml": "value: overlay\n", "overlays/candidate/_meta.yaml": "match: dev\n"}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New[map[string]string](context.Background(), WithDir(dir), WithProfile(Profile{Names: []string{"prod"}}), WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := os.WriteFile(filepath.Join(dir, "overlays/candidate/_meta.yaml"), []byte("match: prod\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if (*m.Get())["value"] == "overlay" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("meta edit was missed; explicit Reload produces %s", (*m.Get())["value"])
}
