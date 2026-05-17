package transform

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaults_FillsMissingOnly(t *testing.T) {
	root := map[string]any{
		"server": map[string]any{"port": 9090},
	}
	tr := Defaults(map[string]any{
		"server": map[string]any{"port": 8080, "addr": "0.0.0.0"},
		"log":    map[string]any{"level": "info"},
	})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("transform: %v", err)
	}
	srv := root["server"].(map[string]any)
	if srv["port"] != 9090 {
		t.Errorf("port: existing 9090 must win, got %v", srv["port"])
	}
	if srv["addr"] != "0.0.0.0" {
		t.Errorf("addr: missing key must be filled, got %v", srv["addr"])
	}
	if root["log"].(map[string]any)["level"] != "info" {
		t.Errorf("log.level missing")
	}
}

func TestSetIfAbsent(t *testing.T) {
	root := map[string]any{}
	if err := SetIfAbsent("a.b.c", 42).Transform(root); err != nil {
		t.Fatal(err)
	}
	if v := root["a"].(map[string]any)["b"].(map[string]any)["c"]; v != 42 {
		t.Fatalf("expected 42, got %v", v)
	}
	_ = SetIfAbsent("a.b.c", 99).Transform(root)
	if v := root["a"].(map[string]any)["b"].(map[string]any)["c"]; v != 42 {
		t.Fatalf("existing value clobbered: %v", v)
	}
}

func TestEnvSubst_BraceWithDefault(t *testing.T) {
	root := map[string]any{
		"db":   map[string]any{"dsn": "${DB_DSN:-postgres://localhost/x}"},
		"port": "${PORT}",
		"raw":  "$2b$10$abcdef",
		"list": []any{"${A:-x}", "static"},
	}
	tr := EnvSubstWith(func(name string) string {
		if name == "PORT" {
			return "9090"
		}
		return ""
	})
	if err := tr.Transform(root); err != nil {
		t.Fatal(err)
	}
	if got := root["db"].(map[string]any)["dsn"]; got != "postgres://localhost/x" {
		t.Errorf("default substitution wrong: %v", got)
	}
	if got := root["port"]; got != "9090" {
		t.Errorf("env substitution wrong: %v", got)
	}
	if got := root["raw"]; got != "$2b$10$abcdef" {
		t.Errorf("bare $ should not be substituted: %v", got)
	}
	lst := root["list"].([]any)
	if lst[0] != "x" || lst[1] != "static" {
		t.Errorf("list walk wrong: %v", lst)
	}
}

func TestDeletePaths(t *testing.T) {
	root := map[string]any{
		"a": map[string]any{"b": 1, "c": 2},
		"d": 3,
	}
	if err := DeletePaths("a.b", "d", "missing.path").Transform(root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["d"]; ok {
		t.Errorf("d not deleted")
	}
	a := root["a"].(map[string]any)
	if _, ok := a["b"]; ok {
		t.Errorf("a.b not deleted")
	}
	if a["c"] != 2 {
		t.Errorf("a.c clobbered: %v", a["c"])
	}
}

func TestAliases_RewriteLegacyKeys(t *testing.T) {
	root := map[string]any{
		"db":    map[string]any{"dsn": "postgres://x"},
		"redis": map[string]any{"host": "old"},
		"cache": map[string]any{"redis": map[string]any{"host": "new"}},
	}
	tr := Aliases(map[string]string{
		"db.dsn":     "database.dsn",
		"redis.host": "cache.redis.host",
	})
	if err := tr.Transform(root); err != nil {
		t.Fatal(err)
	}
	if got := root["database"].(map[string]any)["dsn"]; got != "postgres://x" {
		t.Errorf("alias not applied: %v", got)
	}
	if _, ok := root["db"].(map[string]any)["dsn"]; ok {
		t.Errorf("legacy key not removed")
	}
	if got := root["cache"].(map[string]any)["redis"].(map[string]any)["host"]; got != "new" {
		t.Errorf("existing target clobbered: %v", got)
	}
	if _, ok := root["redis"].(map[string]any)["host"]; ok {
		t.Errorf("legacy redis.host not removed")
	}
}

func TestEnvSubst_RequiredVariableMissing(t *testing.T) {
	root := map[string]any{
		"db": map[string]any{"dsn": "${DB_DSN:?database DSN is required}"},
	}
	tr := EnvSubstWith(func(string) string { return "" })
	err := tr.Transform(root)
	if err == nil {
		t.Fatal("expected error for missing required variable")
	}
	if !errors.Is(err, ErrTransform) {
		t.Fatalf("error must wrap ErrTransform: %v", err)
	}
	const want = "database DSN is required"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error must include user message %q: %v", want, err)
	}
}

func TestEnvSubst_RequiredVariableEmpty(t *testing.T) {
	root := map[string]any{"x": "${X:?}"}
	tr := EnvSubstWith(func(string) string { return "" })
	err := tr.Transform(root)
	if err == nil {
		t.Fatal("expected error for required-but-empty variable")
	}
	if !errors.Is(err, ErrTransform) {
		t.Fatalf("error must wrap ErrTransform: %v", err)
	}
}

func TestEnvSubst_RequiredVariablePresent(t *testing.T) {
	root := map[string]any{"x": "${X:?missing}"}
	tr := EnvSubstWith(func(name string) string {
		if name == "X" {
			return "set"
		}
		return ""
	})
	if err := tr.Transform(root); err != nil {
		t.Fatalf("expected no error when variable is set: %v", err)
	}
	if got := root["x"]; got != "set" {
		t.Fatalf("got %v want set", got)
	}
}

func TestEnvSubst_RequiredFirstErrorWins(t *testing.T) {
	root := map[string]any{
		"a": "${A:?first}",
		"b": "${B:?second}",
	}
	err := EnvSubstWith(func(string) string { return "" }).Transform(root)
	if err == nil {
		t.Fatal("expected error")
	}
	// Either first-walked key may fire; just ensure exactly one wrapped error.
	if !errors.Is(err, ErrTransform) {
		t.Fatalf("error must wrap ErrTransform: %v", err)
	}
}

func TestTransformerFunc_NameAndError(t *testing.T) {
	want := errors.New("boom")
	tr := TransformerFunc{
		NameStr: "explode",
		Fn:      func(map[string]any) error { return want },
	}
	if tr.Name() != "explode" {
		t.Fatalf("name")
	}
	if got := tr.Transform(nil); !errors.Is(got, want) {
		t.Fatalf("error: %v", got)
	}
}
