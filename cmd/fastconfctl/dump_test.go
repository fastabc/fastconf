package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDumpCommandPreservesInteger(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "base"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "00.json"), []byte(`{"id":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "fastconfctl")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, "dump", "-dir", dir, "-format", format)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dump: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "9007199254740993") || strings.Contains(string(out), `"9007199254740993"`) {
				t.Fatalf("changed integer: %s", out)
			}
		})
	}
}
