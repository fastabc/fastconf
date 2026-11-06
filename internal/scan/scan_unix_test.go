//go:build unix

package scan

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScan_SkipsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "base"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "base", "00.yaml"), []byte("a: 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "base", "10.yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		layers, err := collectLayers(root, Options{})
		if err == nil && len(layers) != 1 {
			err = os.ErrInvalid
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan blocked on FIFO")
	}
	if _, err := collectLayers(root, Options{Strict: true}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("strict: %v", err)
	}
}

func TestScan_SymlinkToRegularFileLoads(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"..data", "base"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "..data", "app.yaml"), []byte("a: 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "..data", "app.yaml"), filepath.Join(root, "base", "app.yaml")); err != nil {
		t.Fatal(err)
	}
	if layers, err := collectLayers(root, Options{}); err != nil || len(layers) != 1 {
		t.Fatalf("layers=%v err=%v", layers, err)
	}
}
