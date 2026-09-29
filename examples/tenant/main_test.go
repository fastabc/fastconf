package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantIsolation(t *testing.T) {
	t.Setenv("FC_TENANT_A_DATABASE_POOL", "12")
	t.Setenv("FC_TENANT_B_DATABASE_POOL", "24")
	var out bytes.Buffer
	if err := run(context.Background(), "conf.d", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tenant-a addr=:8081 timeout=30s pool=12 rps=100\n",
		"tenant-b addr=:8082 timeout=30s pool=24 rps=200\n",
		"tenant-a dsn=***REDACTED***\n",
		"tenant-b dsn=***REDACTED***\n",
		"tenant-a rejected=true preserved=true tenant-b untouched=true\n",
		"tenant-b updated rps=250 tenant-a rps=100\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, &out)
		}
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatal("diagnostic output leaked a DSN")
	}
}

func TestMissingTenantFile(t *testing.T) {
	if err := run(context.Background(), t.TempDir(), &bytes.Buffer{}); err == nil {
		t.Fatal("missing required tenant file should fail initialization")
	}
}

func TestEditableLocalConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("conf.d")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tenants", "tenant-a.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("rps: 100"), []byte("rps: 125"), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), dir, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tenant-b updated rps=250 tenant-a rps=125\n") {
		t.Fatalf("local edit did not remain isolated:\n%s", &out)
	}
}
