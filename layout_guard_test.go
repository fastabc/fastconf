package fastconf_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLayoutGuard enforces the historical canonical-file decisions that are
// still intentional today. It should not be read as a blanket ban on every
// future split in the root package.
func TestLayoutGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	type rule struct {
		canonical       string
		forbiddenPrefix string
		docref          string
	}
	rules := []rule{
		{"options.go", "opt_", "SPEC-90"},
		{"manager.go", "manager_", "Wave F"},
		{"errors.go", "failure_", "SPEC-97"},
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	for _, r := range rules {
		if !have[r.canonical] {
			t.Fatalf("%s missing — required by %s", r.canonical, r.docref)
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			if strings.HasPrefix(n, r.forbiddenPrefix) {
				t.Fatalf("%s violates %s — merge into %s", n, r.docref, r.canonical)
			}
		}
	}

	// Also forbid bug_*_test.go files (SPEC-100).
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bug_") {
			t.Fatalf("%s violates SPEC-100 — fold into the topic test file", e.Name())
		}
		if strings.HasPrefix(e.Name(), "example_") && e.Name() != "example_api_test.go" {
			t.Fatalf("%s belongs under examples/; keep only root package godoc examples here", e.Name())
		}
	}
}
