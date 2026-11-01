// Package testutil supplies shared filesystem, polling and Provider test fixtures.
package testutil

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// WriteFile creates parent directories as needed and writes content to p.
// The test is fatal-failed on any error.
func WriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("WriteFile mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

// TempConf writes relative-path/content entries under t.TempDir and returns its root.
// Directories and cleanup are automatic; write failures fail the test.
func TempConf(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		WriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

// WaitFor polls cond every 10 ms until it returns true or timeout elapses.
// The test is fatal-failed with msg if the deadline is exceeded.
func WaitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("WaitFor timed out after %s: %s", timeout, msg)
}

// FakeProvider is a stub contracts.Provider backed by a static map.
// It is load-only, without native notifications. Useful for unit tests
// that need a provider without spinning up a real remote source.
type FakeProvider struct {
	name     string
	priority int
	data     map[string]any
	// LoadErr, if non-nil, is returned by Load instead of data.
	LoadErr error
}

// NewFakeProvider constructs a FakeProvider with the given name, priority
// and static data.
func NewFakeProvider(name string, priority int, data map[string]any) *FakeProvider {
	return &FakeProvider{name: name, priority: priority, data: data}
}

func (f *FakeProvider) Name() string { return f.name }
func (f *FakeProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: f.priority}
}
func (f *FakeProvider) Load(context.Context) (contracts.Snapshot, error) {
	if f.LoadErr != nil {
		return contracts.Snapshot{}, f.LoadErr
	}
	// Isolate top-level keys; nested values remain shared with the stub.
	return contracts.Snapshot{Map: maps.Clone(f.data)}, nil
}
