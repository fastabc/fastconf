package dotenv

import (
	"context"
	"os"
	"reflect"
	"testing"

	envprovider "github.com/fastabc/fastconf/providers/env"
)

func TestProvider_LoadParsesDotEnv(t *testing.T) {
	path := writeTempDotEnv(t, "# comment\nAPP_PORT=8080\nAPP_DATABASE__HOST=localhost\nAPP_DATABASE__PASS='secret'\nAPP_DEBUG=\"true\"\nexport APP_RATE=1.5\n")
	p := New("APP_", path).WithLookup(func(string) (string, bool) { return "", false })
	snap, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"port": "8080", "database": map[string]any{"host": "localhost", "pass": "secret"}, "debug": "true", "rate": "1.5"}
	if !reflect.DeepEqual(snap.Map, want) {
		t.Fatalf("Load=%#v; want %#v", snap.Map, want)
	}
}

func TestProvider_EnvPresenceWinsEvenWhenEmpty(t *testing.T) {
	path := writeTempDotEnv(t, "APP_PORT=9999\nAPP_HOST=localhost\n")
	p := New("APP_", path).WithLookup(func(k string) (string, bool) {
		if k == "APP_PORT" {
			return "", true
		}
		return "", false
	})
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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

func TestProvider_EnvAbsenceFallsBackToDotEnv(t *testing.T) {
	path := writeTempDotEnv(t, "APP_PORT=9999\n")
	p := New("APP_", path).WithLookup(func(string) (string, bool) {
		return "", false
	})
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "9999" {
		t.Errorf("port = %v want 9999", got["port"])
	}
}

func TestProvider_WithLookupNilRestoresDefault(t *testing.T) {
	t.Setenv("APP_PORT", "")
	path := writeTempDotEnv(t, "APP_PORT=9999\n")
	p := New("APP_", path).
		WithLookup(func(string) (string, bool) { return "", false }).
		WithLookup(nil)
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["port"]; ok {
		t.Error("nil lookup should restore os.LookupEnv and keep explicit empty env authoritative")
	}
}

func TestProvider_NoPrefixFilter(t *testing.T) {
	p := New("", writeTempDotEnv(t, "PORT=8080\nDATABASE_HOST=local\n")).WithLookup(func(string) (string, bool) { return "", false })
	snap, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"port": "8080", "database": map[string]any{"host": "local"}}
	if !reflect.DeepEqual(snap.Map, want) {
		t.Fatalf("Load=%#v; want %#v", snap.Map, want)
	}
}

func TestProvider_MissingFile(t *testing.T) {
	p := New("APP_", "/nonexistent/.env").
		WithLookup(func(string) (string, bool) { return "", false })
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
	if err != nil {
		t.Fatalf("Load with missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

// Default DotReplacer end-to-end: APP_DATABASE_DSN nests under "database".
func TestProvider_DotReplacerLoad(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE_DSN=postgres://x\nAPP_PORT=8080\n")
	p := New("APP_", path).WithLookup(func(string) (string, bool) { return "", false })
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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
func TestProvider_DoubleUnderscoreReplacer(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE__POOL=20\nAPP_FEATURE_FLAGS=on\n")
	p := New("APP_", path).
		WithReplacer(envprovider.DoubleUnderscoreReplacer).
		WithLookup(func(string) (string, bool) { return "", false })
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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
func TestProvider_AtNamespaces(t *testing.T) {
	path := writeTempDotEnv(t, "APP_DATABASE_DSN=postgres://x\n")
	p := New("APP_", path).
		At("config.runtime").
		WithLookup(func(string) (string, bool) { return "", false })
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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
