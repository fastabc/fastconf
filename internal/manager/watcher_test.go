package manager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	iopts "github.com/fastabc/fastconf/internal/options"
)

type watchPathTestProvider struct {
	paths []string
}

func (p *watchPathTestProvider) Name() string  { return "watch-path-test" }
func (p *watchPathTestProvider) Priority() int { return contracts.PriorityStatic }
func (p *watchPathTestProvider) Load(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (p *watchPathTestProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}
func (p *watchPathTestProvider) WatchPaths() []string { return p.paths }

func TestCollectWatchPaths_IncludesProviderWatchPaths(t *testing.T) {
	root := t.TempDir()
	labels := filepath.Join(root, "podinfo", "labels")
	annotations := filepath.Join(root, "podinfo", "annotations")

	got := collectWatchPaths(iopts.Options{
		Dir: root,
		Providers: []contracts.Provider{
			&watchPathTestProvider{paths: []string{labels, annotations, labels}},
		},
	})

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

func TestPriorityOverride_PreservesWatchPaths(t *testing.T) {
	p := iopts.WrapWithPriority(&watchPathTestProvider{paths: []string{"/tmp/a"}}, contracts.PriorityCLI)
	wp, ok := p.(contracts.WatchPathProvider)
	if !ok {
		t.Fatal("wrapped provider lost WatchPathProvider")
	}
	got := wp.WatchPaths()
	if len(got) != 1 || got[0] != "/tmp/a" {
		t.Fatalf("WatchPaths() = %v want [/tmp/a]", got)
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
