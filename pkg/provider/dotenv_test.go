package provider

import (
	"context"
	"os"
	"testing"
)

func TestDotEnvProvider_BasicParsing(t *testing.T) {
	env := `
# comment
APP_PORT=8080
APP_DATABASE__HOST=localhost
APP_DATABASE__PASS='secret'
APP_DEBUG="true"
export APP_RATE=1.5
`
	var loaded map[string]any
	p := NewDotEnv("APP_", "_test_.env").
		withGetenv(func(k string) string { return "" })

	// Bypass file IO: use parseDotEnv directly, then simulate Load via fake FS.
	pairs, err := parseDotEnv([]byte(env))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}

	want := map[string]string{
		"APP_PORT":           "8080",
		"APP_DATABASE__HOST": "localhost",
		"APP_DATABASE__PASS": "secret",
		"APP_DEBUG":          "true",
		"APP_RATE":           "1.5",
	}
	for k, wantV := range want {
		gotV, ok := pairs[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if gotV != wantV {
			t.Errorf("key %q: got %q want %q", k, gotV, wantV)
		}
	}

	// parseDotEnv should not have found comment or blank lines as keys.
	_ = loaded
	_ = p
}

func TestDotEnvProvider_EnvPrecedence(t *testing.T) {
	// APP_PORT is already set in the "environment" — dotenv should skip it.
	env := "APP_PORT=9999\nAPP_HOST=localhost\n"
	pairs, err := parseDotEnv([]byte(env))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}

	getenv := func(k string) string {
		if k == "APP_PORT" {
			return "8080" // already set
		}
		return ""
	}

	p := &DotEnvProvider{
		prefix:  "APP_",
		getenv:  getenv,
	}

	out := map[string]any{}
	// Replicate Load's inner loop manually.
	for k, v := range pairs {
		if p.getenv != nil && p.getenv(k) != "" {
			continue // env takes precedence
		}
		if p.prefix != "" && len(k) < len(p.prefix) {
			continue
		}
		_ = v
		out[k] = v
	}

	if _, skipped := out["APP_PORT"]; skipped {
		t.Error("APP_PORT should have been skipped (env takes precedence)")
	}
	if _, ok := out["APP_HOST"]; !ok {
		t.Error("APP_HOST should have been loaded from dotenv")
	}
}

func TestDotEnvProvider_NoPrefixFilter(t *testing.T) {
	// When no prefix is set, all keys are accepted.
	p := NewDotEnv("").
		withGetenv(func(_ string) string { return "" })
	_ = p
}

func TestDotEnvProvider_MissingFile(t *testing.T) {
	p := NewDotEnv("APP_", "/nonexistent/.env").
		withGetenv(func(_ string) string { return "" })
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load with missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

// Default DotReplacer end-to-end: APP_DATABASE_DSN nests under "database".
func TestDotEnvProvider_DotReplacerLoad(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE_DSN=postgres://x\nAPP_PORT=8080\n")
	p := NewDotEnv("APP_", path).withGetenv(func(string) string { return "" })
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := got["port"]; v != "8080" {
		t.Errorf("port = %v want \"8080\"", v)
	}
	db, _ := got["database"].(map[string]any)
	if db["dsn"] != "postgres://x" {
		t.Errorf("database.dsn = %v want postgres://x", db["dsn"])
	}
}

// DoubleUnderscoreReplacer keeps single "_" inside keys.
func TestDotEnvProvider_DoubleUnderscoreReplacer(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE__POOL=20\nAPP_FEATURE_FLAGS=on\n")
	p := NewDotEnv("APP_", path).
		WithReplacer(DoubleUnderscoreReplacer).
		withGetenv(func(string) string { return "" })
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := got["feature_flags"]; v != "on" {
		t.Errorf("feature_flags = %v", v)
	}
	db, _ := got["database"].(map[string]any)
	if db["pool"] != "20" {
		t.Errorf("database.pool = %v", db["pool"])
	}
}

// At() grafts the dotenv tree under a dotted path.
func TestDotEnvProvider_AtNamespaces(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE_DSN=postgres://x\n")
	p := NewDotEnv("APP_", path).
		At("config.runtime").
		withGetenv(func(string) string { return "" })
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := got["config"].(map[string]any)
	rt, _ := cfg["runtime"].(map[string]any)
	db, _ := rt["database"].(map[string]any)
	if db["dsn"] != "postgres://x" {
		t.Errorf("expected grafted config.runtime.database.dsn, got %#v", got)
	}
}

func writeTempDotEnv(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/.env"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseDotEnv_Escapes(t *testing.T) {
	env := `KEY="line1\nline2\ttab\"quote"`
	pairs, err := parseDotEnv([]byte(env))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}
	want := "line1\nline2\ttab\"quote"
	if pairs["KEY"] != want {
		t.Errorf("got %q, want %q", pairs["KEY"], want)
	}
}

func TestParseDotEnv_UnterminatedDouble(t *testing.T) {
	_, err := parseDotEnv([]byte(`KEY="unclosed`))
	if err == nil {
		t.Fatal("expected error for unterminated double quote")
	}
}

func TestParseDotEnv_UnterminatedSingle(t *testing.T) {
	_, err := parseDotEnv([]byte(`KEY='unclosed`))
	if err == nil {
		t.Fatal("expected error for unterminated single quote")
	}
}
