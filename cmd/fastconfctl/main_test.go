package main

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

// Flag-default tests for dir/profile/provider live in
// cmd/internal/cli/setup_test.go (TestRegisterFlags_Defaults +
// TestProviderFlags_Apply_*). This file only tests the JSON diff helper
// which is local to fastconfctl.
func TestBuildJSONChanges(t *testing.T) {
	a := map[string]any{"x": 1.0, "y": "old", "z": "same"}
	b := map[string]any{"x": 2.0, "y": "new", "w": "added"}
	changes := buildJSONChanges("", a, b)
	ops := map[string]string{}
	for _, c := range changes {
		ops[c["path"].(string)] = c["op"].(string)
	}
	if ops["x"] != "~" {
		t.Errorf("x: want ~, got %q", ops["x"])
	}
	if ops["y"] != "~" {
		t.Errorf("y: want ~, got %q", ops["y"])
	}
	if ops["z"] != "-" {
		t.Errorf("z: want -, got %q", ops["z"])
	}
	if ops["w"] != "+" {
		t.Errorf("w: want +, got %q", ops["w"])
	}
}

func TestMainDoesNotDefineLocalLookupPath(t *testing.T) {
	if packageSourceContains(t, "func lookupPath(") {
		t.Fatal("fastconfctl must use pkg/mappath.GetDotted instead of a local lookupPath")
	}
}

// TestCLIFlagsParityWithInternalCLI pins the default values of the shared
// cli.Flags type from cmd/internal/cli. fastconfctl now directly imports
// that package, so parity is guaranteed at compile time.
func TestCLIFlagsParityWithInternalCLI(t *testing.T) {
	var f cli.Flags
	cli.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), &f)
	if f.Dir != fastconf.DefaultDir {
		t.Errorf("Dir default: got %q, want fastconf.DefaultDir (%q)", f.Dir, fastconf.DefaultDir)
	}
	if f.Profile != "" {
		t.Errorf("Profile default: got %q, want empty string", f.Profile)
	}
	if f.Strict {
		t.Error("Strict default: got true, want false")
	}
	if f.Watch {
		t.Error("Watch default: got true, want false")
	}
}

func packageSourceContains(t *testing.T, needle string) bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), needle) {
			return true
		}
	}
	return false
}
