package fastconf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

type yamlCfg struct {
	Server struct {
		Addr string `json:"addr"`
		Port int    `json:"port"`
	} `json:"server"`
	Database struct {
		DSN string `json:"dsn"`
	} `json:"database"`
}

func TestState_Dump_StableOrder(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
  port: 8080
database:
  dsn: "postgres://prod"
`)},
	}
	mgr, err := New[yamlCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	state := mgr.Snapshot()

	// First call.
	a, err := state.Unredacted().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	// Second call must produce byte-identical output (deterministic order).
	b, err := state.Unredacted().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("Dump(YAML) not stable:\nfirst:\n%s\nsecond:\n%s", a, b)
	}

	// Lexicographic ordering: database key must appear before server.
	out := string(a)
	dbIdx := strings.Index(out, "database:")
	srvIdx := strings.Index(out, "server:")
	if dbIdx < 0 || srvIdx < 0 || dbIdx > srvIdx {
		t.Errorf("expected sorted keys; got order:\n%s", out)
	}
}

// secretYAMLCfg has a fc:"secret" field so we can prove the
// redactor parameter is honoured.
type secretYAMLCfg struct {
	Server struct {
		Addr string `json:"addr"`
	} `json:"server"`
	Database struct {
		DSN string `json:"dsn" fc:"secret"`
	} `json:"database"`
}

func TestState_Dump_HonoursRedactor(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
database:
  dsn: "postgres://user:hunter2@host/db"
`)},
	}
	mgr, err := New[secretYAMLCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	// Without redactor: raw secret leaks.
	raw, err := mgr.Snapshot().Unredacted().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Errorf("baseline (nil redactor) should emit raw secret; got:\n%s", raw)
	}

	// With the default redactor: secret replaced, non-secret untouched.
	masked, err := mgr.Snapshot().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(masked), "hunter2") {
		t.Errorf("default redactor did not mask secret:\n%s", masked)
	}
	if !strings.Contains(string(masked), "REDACTED") {
		t.Errorf("expected REDACTED marker:\n%s", masked)
	}
	if !strings.Contains(string(masked), ":8080") {
		t.Errorf("non-secret field unexpectedly altered:\n%s", masked)
	}

	// WithRedactor controls how secrets display.
	custom, err := New[secretYAMLCfg](context.Background(), WithFS(fs), WithDir("conf.d"),
		WithRedactor(func(path string, _ any) any { return "[secret:" + path + "]" }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = custom.Close() }()
	out, err := custom.Snapshot().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[secret:database.dsn]") {
		t.Errorf("custom redactor did not apply:\n%s", out)
	}
}

func TestState_Dump_NestedShape(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: x\n  port: 1\n")},
	}
	mgr, _ := New[yamlCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
	)
	defer func() { _ = mgr.Close() }()
	out, err := mgr.Snapshot().Unredacted().Dump(YAML)
	if err != nil {
		t.Fatal(err)
	}
	// Output should be nested YAML, not flat dotted keys.
	if !strings.Contains(string(out), "server:\n") {
		t.Errorf("expected nested server: in output:\n%s", out)
	}
	if strings.Contains(string(out), "server.addr") {
		t.Errorf("flat key leaked into YAML output:\n%s", out)
	}
}

// TestState_Dump_JSONParity verifies that Unredacted().Dump(JSON) round-trips
// to the same tree as json.Marshal(*state.Value) does
// (modulo whitespace/ordering — both sides unmarshal to identical maps).
func TestState_Dump_JSONParity(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
  port: 8080
database:
  dsn: "postgres://prod"
`)},
	}
	mgr, err := New[yamlCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	snap := mgr.Snapshot()

	dump, err := snap.Unredacted().Dump(JSON)
	if err != nil {
		t.Fatalf("Dump(JSON): %v", err)
	}
	direct, err := json.Marshal(snap.Value())
	if err != nil {
		t.Fatalf("json.Marshal(Value): %v", err)
	}
	var a, b map[string]any
	if err := json.Unmarshal(dump, &a); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if err := json.Unmarshal(direct, &b); err != nil {
		t.Fatalf("unmarshal direct: %v", err)
	}
	if !mapsEqualJSON(a, b) {
		t.Errorf("Dump(JSON) tree does not match json.Marshal(Value)\ndump: %s\ndirect: %s", dump, direct)
	}
}

func TestState_Dump_TOML(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: x\n  port: 1\ndatabase:\n  dsn: pg\n")},
	}
	mgr, err := New[yamlCfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	out, err := mgr.Snapshot().Unredacted().Dump(TOML)
	if err != nil {
		t.Fatalf("Dump(TOML): %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "[server]") || !strings.Contains(s, "[database]") {
		t.Errorf("expected TOML section headers in:\n%s", s)
	}
}

func TestDump_PropagatesSerializationError(t *testing.T) {
	value := struct{ Unsupported chan int }{make(chan int)}
	s := &State[struct{ Unsupported chan int }]{value: &value, generation: 1}
	for _, format := range []Format{JSON, YAML, TOML} {
		_, err := s.Unredacted().Dump(format)
		var want *json.UnsupportedTypeError
		if !errors.As(err, &want) {
			t.Fatalf("%s hid serialization error: %v", format, err)
		}
	}
}
