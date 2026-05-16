package cli_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/fastabc/fastconf/cmd/internal/cli"
	"github.com/fastabc/fastconf"
)

func TestRegisterFlags_Defaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Dir != fastconf.DefaultDir {
		t.Errorf("Dir default: want %q, got %q", fastconf.DefaultDir, f.Dir)
	}
	if f.Profile != "" {
		t.Errorf("Profile default: want empty, got %q", f.Profile)
	}
	if f.Strict {
		t.Error("Strict default: want false")
	}
	if f.Watch {
		t.Error("Watch default: want false")
	}
	if len(f.Providers) != 0 {
		t.Errorf("Providers default: want empty, got %v", f.Providers)
	}
}

func TestRegisterFlags_ParseAll(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	args := []string{
		"-dir", "/tmp/cfg",
		"-profile", "prod",
		"-strict",
		"-watch",
		"-provider", "env=APP_",
		"-provider", `vault={"path":"secret/db"}`,
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Dir != "/tmp/cfg" || f.Profile != "prod" || !f.Strict || !f.Watch {
		t.Errorf("unexpected flag values: %+v", f)
	}
	if len(f.Providers) != 2 {
		t.Fatalf("Providers: want 2, got %d", len(f.Providers))
	}
}

func TestLoadConfig_WithDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "base", "10-app.yaml"), "name: testapp\nport: 8080\n")
	mgr, err := cli.LoadConfig[map[string]any](context.Background(), cli.Flags{Dir: dir})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	defer mgr.Close()
	got := mgr.Get()
	if got == nil {
		t.Fatal("Get: nil")
	}
	if (*got)["name"] != "testapp" {
		t.Errorf("name: want testapp, got %v", (*got)["name"])
	}
}

func TestLoadConfig_NotADir(t *testing.T) {
	_, err := cli.LoadConfig[map[string]any](context.Background(), cli.Flags{Dir: "/no/such/path"})
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestProviderFlags_Apply_EnvAndJSON(t *testing.T) {
	pf := cli.ProviderFlags{"env=APP_", `mock={"x":1}`}
	var opts []fastconf.Option
	if err := pf.Apply(&opts); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("opts: want 2, got %d", len(opts))
	}
}

func TestProviderFlags_Apply_BadSpec(t *testing.T) {
	pf := cli.ProviderFlags{"=missing-name"}
	var opts []fastconf.Option
	if err := pf.Apply(&opts); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
