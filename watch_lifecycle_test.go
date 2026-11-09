package fastconf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

type watchGapGenerator struct {
	path  string
	armed atomic.Bool
}

func (*watchGapGenerator) Name() string { return "watch-gap" }
func (g *watchGapGenerator) Generate(context.Context) ([]contracts.RawLayer, error) {
	if g.armed.CompareAndSwap(true, false) {
		return nil, os.WriteFile(g.path, []byte("value: latest\n"), 0o600)
	}
	return nil, nil
}

func TestFileWatch_DynamicDirectoryReadGap(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"base/a.yaml": "value: base\n", "custom/a.yaml": "value: intermediate\n"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g := &watchGapGenerator{path: filepath.Join(dir, "custom", "a.yaml")}
	m, err := New[map[string]string](context.Background(), WithDir(dir), WithGenerator(g), WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	// The generator writes after discovery reads the newly selected directory.
	g.armed.Store(true)
	if err := os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte("spec:\n  baseDir: custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if (*m.Get())["value"] == "latest" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missed dynamic-directory update: %v", *m.Get())
}

func TestNewFailedWatchInitializationCleansWorkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "base", "a.yaml")
	if err := os.WriteFile(path, []byte("value: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	failure := errors.New("reject initial configuration")
	for range 16 {
		m, err := New[map[string]string](context.Background(), WithDir(dir), WithWatch(Watch{Quiet: time.Millisecond}), WithValidate(func(*map[string]string) error {
			if err := os.WriteFile(path, []byte("value: new\n"), 0o600); err != nil {
				return err
			}
			return failure
		}))
		if m != nil {
			_ = m.Close()
			t.Fatal("failed initialization returned a manager")
		}
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workers retained after failed construction: before=%d after=%d", before, runtime.NumGoroutine())
}
