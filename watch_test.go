package fastconf

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

type watchPathTestProvider struct {
	paths []string
}

func (p *watchPathTestProvider) Name() string { return "watch-path-test" }

func (p *watchPathTestProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: contracts.PriorityStatic, WatchPaths: p.paths}
}

func (p *watchPathTestProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{}}, nil
}

func TestCollectWatchPaths_IncludesProviderWatchPaths(t *testing.T) {
	root := t.TempDir()
	labels := filepath.Join(root, "podinfo", "labels")
	annotations := filepath.Join(root, "podinfo", "annotations")

	o := &options{Dir: root}
	o.addProvider(&watchPathTestProvider{paths: []string{labels, annotations, labels}})
	got := collectWatchPaths(o)

	for _, want := range []string{labels, annotations} {
		abs, err := filepath.Abs(want)
		if err != nil {
			t.Fatal(err)
		}
		if !containsPath(got, abs) {
			t.Fatalf("collectWatchPaths() = %v; missing %q", got, abs)
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func TestWatch_PauseResume(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: x\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if mgr.Paused() {
		t.Fatal("default should not be paused")
	}
	mgr.Pause()
	if !mgr.Paused() {
		t.Fatal("expected paused")
	}
	mgr.Resume()
	if mgr.Paused() {
		t.Fatal("expected resumed")
	}
}

type failingWatchProvider struct{ attempted chan struct{} }

func (p failingWatchProvider) Name() string { return "failing" }

func (p failingWatchProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 0}
}

func (p failingWatchProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"v": 1}}, nil
}

func (p failingWatchProvider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	p.attempted <- struct{}{}
	return nil, errors.New("offline")
}

func TestProviderWatch_CloseStopsRetryLoop(t *testing.T) {
	p := failingWatchProvider{make(chan struct{}, 1)}
	m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{}), WithProvider(p))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	select {
	case <-p.attempted:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not start")
	}
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel watcher backoff")
	}
}
