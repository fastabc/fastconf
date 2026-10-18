package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
)

func TestFileWatchRetainsLastValidValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("port: 8080")
	m, err := newManager(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	commits := make(chan int, 4)
	defer fastconf.Subscribe(m, func(c *Config) *int { return &c.Port }, func(_, next *int) { commits <- *next })()
	write("port: 9090")
	select {
	case port := <-commits:
		if port != 9090 {
			t.Fatal(port)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no file update")
	}
	before := m.Snapshot()
	write("prot: 9091")
	select {
	case <-m.Errors():
		if m.Snapshot() != before {
			t.Fatal("invalid file replaced snapshot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no rejection")
	}
}
