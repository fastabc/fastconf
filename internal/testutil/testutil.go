// Package testutil centralises test helpers shared across the fastconf
// module. It is an internal package so it cannot be imported by
// external code; sub-packages that need these helpers add:
//
//	import "github.com/fastabc/fastconf/internal/testutil"
//
// Three categories of helpers are provided:
//
//  1. File-system setup — WriteFile, TempConf
//  2. Polling — WaitFor
//  3. Provider stubs — FakeProvider
package testutil

import (
	"context"
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

// TempConf creates a temporary directory, populates it with files whose
// paths (relative to the temp root) and contents are given by the files
// map, and returns the temp root. The caller may use t.TempDir()-based
// registration for cleanup — this function registers nothing itself.
//
// Example:
//
//	root := testutil.TempConf(t, map[string]string{
//	    "conf.d/base/00-base.yaml": "port: 8080\n",
//	    "conf.d/overlays/prod/01-prod.yaml": "port: 443\n",
//	})
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
// Watch returns nil (no change notifications). Useful for unit tests
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

func (f *FakeProvider) Name() string     { return f.name }
func (f *FakeProvider) Priority() int    { return f.priority }
func (f *FakeProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
func (f *FakeProvider) Load(_ context.Context) (map[string]any, error) {
	if f.LoadErr != nil {
		return nil, f.LoadErr
	}
	// Return a shallow copy so callers cannot mutate the stub's data.
	out := make(map[string]any, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out, nil
}
