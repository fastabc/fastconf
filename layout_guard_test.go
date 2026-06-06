package fastconf_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLayoutGuard enforces the canonical root-package file layout. It should
// not be read as a blanket ban on every future split in the root package.
func TestLayoutGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	type rule struct {
		canonical       string
		forbiddenPrefix string
	}
	rules := []rule{
		{"options.go", "opt_"},
		{"manager.go", "manager_"},
		{"errors.go", "failure_"},
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	for _, r := range rules {
		if !have[r.canonical] {
			t.Fatalf("%s missing", r.canonical)
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			if strings.HasPrefix(n, r.forbiddenPrefix) {
				t.Fatalf("%s should be folded into %s", n, r.canonical)
			}
		}
	}

	// Keep regression coverage in the topic test file instead of bug_* files.
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bug_") {
			t.Fatalf("%s should be folded into the topic test file", e.Name())
		}
		if strings.HasPrefix(e.Name(), "example_") && e.Name() != "example_api_test.go" {
			t.Fatalf("%s belongs under examples/; keep only root package godoc examples here", e.Name())
		}
	}
}
