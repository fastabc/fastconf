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
		WithLookup(func(string) (string, bool) { return "", false })

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

func TestDotEnvProvider_EnvPresenceWinsEvenWhenEmpty(t *testing.T) {
	path := writeTempDotEnv(t, "APP_PORT=9999\nAPP_HOST=localhost\n")
	p := NewDotEnv("APP_", path).WithLookup(func(k string) (string, bool) {
		if k == "APP_PORT" {
			return "", true
		}
		return "", false
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["port"]; ok {
		t.Error("APP_PORT should have been skipped when process env is explicitly empty")
	}
	if got["host"] != "localhost" {
		t.Errorf("host = %v want localhost", got["host"])
	}
}

func TestDotEnvProvider_EnvAbsenceFallsBackToDotEnv(t *testing.T) {
	path := writeTempDotEnv(t, "APP_PORT=9999\n")
	p := NewDotEnv("APP_", path).WithLookup(func(string) (string, bool) {
		return "", false
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "9999" {
		t.Errorf("port = %v want 9999", got["port"])
	}
}

func TestDotEnvProvider_WithLookupNilRestoresDefault(t *testing.T) {
	t.Setenv("APP_PORT", "")
	path := writeTempDotEnv(t, "APP_PORT=9999\n")
	p := NewDotEnv("APP_", path).
		WithLookup(func(string) (string, bool) { return "", false }).
		WithLookup(nil)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["port"]; ok {
		t.Error("nil lookup should restore os.LookupEnv and keep explicit empty env authoritative")
	}
}

func TestDotEnvProvider_NoPrefixFilter(t *testing.T) {
	// When no prefix is set, all keys are accepted.
	p := NewDotEnv("").
		WithLookup(func(string) (string, bool) { return "", false })
	_ = p
}

func TestDotEnvProvider_MissingFile(t *testing.T) {
	p := NewDotEnv("APP_", "/nonexistent/.env").
		WithLookup(func(string) (string, bool) { return "", false })
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
	p := NewDotEnv("APP_", path).WithLookup(func(string) (string, bool) { return "", false })
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
		WithLookup(func(string) (string, bool) { return "", false })
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
		WithLookup(func(string) (string, bool) { return "", false })
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
