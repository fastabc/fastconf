package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/pkg/provider"
)

func TestEnvKeyReplacer_DefaultDotToUnderscore(t *testing.T) {
	t.Setenv("EBT_SERVER_ADDR", "0.0.0.0:8080")
	t.Setenv("EBT_SERVER_PORT", "9000")
	t.Setenv("EBT_DATABASE_DSN", "postgres://localhost/db")

	p := provider.NewEnvReplacer("EBT_", provider.DotReplacer)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server map, got %T", got["server"])
	}
	if server["addr"] != "0.0.0.0:8080" {
		t.Errorf("server.addr = %v, want 0.0.0.0:8080", server["addr"])
	}
	if server["port"] != int64(9000) {
		t.Errorf("server.port = %v (%T), want int64(9000)", server["port"], server["port"])
	}
	database, ok := got["database"].(map[string]any)
	if !ok {
		t.Fatalf("expected database map, got %T", got["database"])
	}
	if database["dsn"] != "postgres://localhost/db" {
		t.Errorf("database.dsn = %v, want postgres://localhost/db", database["dsn"])
	}
}

func TestEnvKeyReplacer_CustomReplacer(t *testing.T) {
	// Funky convention: prefix-stripped name has segments separated by "x".
	t.Setenv("ZZ_SERVERxADDR", "1.2.3.4")
	custom := provider.EnvKeyReplacerFunc(func(s string) string {
		out := []byte{}
		for _, c := range []byte(s) {
			switch c {
			case 'x', 'X':
				out = append(out, '.')
			default:
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				out = append(out, c)
			}
		}
		return string(out)
	})
	p := provider.NewEnvReplacer("ZZ_", custom)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server map, got %T", got["server"])
	}
	if server["addr"] != "1.2.3.4" {
		t.Errorf("server.addr = %v, want 1.2.3.4", server["addr"])
	}
}

func TestEnvKeyReplacer_NilReplacerUsesDefault(t *testing.T) {
	p := provider.NewEnvReplacer("NN_", nil)
	if p == nil {
		t.Fatal("NewEnvReplacer returned nil")
	}
	if !strings.Contains(p.Name(), "env-replacer:NN_") {
		t.Errorf("unexpected name: %q", p.Name())
	}
	// Sanity-check Load runs without env vars.
	if _, err := p.Load(context.Background()); err != nil {
		t.Errorf("Load: %v", err)
	}
}
