package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpCommandPreservesInteger(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "base"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "00.json"), []byte(`{"id":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		cmd := exec.Command("go", "run", ".", "dump", "-dir", dir, "-format", format)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("dump: %v", err)
		}
		if !strings.Contains(string(out), "9007199254740993") || strings.Contains(string(out), `"9007199254740993"`) {
			t.Fatalf("%s changed integer: %s", format, out)
		}
	}
}
