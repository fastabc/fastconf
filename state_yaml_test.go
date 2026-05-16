package fastconf_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
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

func TestState_MarshalYAML_StableOrder(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
  port: 8080
database:
  dsn: "postgres://prod"
`)},
	}
	mgr, err := fastconf.New[yamlCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	state := mgr.Snapshot()

	// First call.
	a, err := state.MarshalYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Second call must produce byte-identical output (deterministic order).
	b, err := state.MarshalYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("MarshalYAML not stable:\nfirst:\n%s\nsecond:\n%s", a, b)
	}

	// Lexicographic ordering: database key must appear before server.
	out := string(a)
	dbIdx := strings.Index(out, "database:")
	srvIdx := strings.Index(out, "server:")
	if dbIdx < 0 || srvIdx < 0 || dbIdx > srvIdx {
		t.Errorf("expected sorted keys; got order:\n%s", out)
	}
}

func TestState_MarshalYAML_NilState(t *testing.T) {
	var s *fastconf.State[yamlCfg]
	b, err := s.MarshalYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}\n" {
		t.Errorf("nil state should marshal to empty map, got %q", b)
	}
}

// secretYAMLCfg has a fastconf:"secret" field so we can prove the
// redactor parameter is honoured (P2.3).
type secretYAMLCfg struct {
	Server struct {
		Addr string `json:"addr"`
	} `json:"server"`
	Database struct {
		DSN string `json:"dsn" fastconf:"secret"`
	} `json:"database"`
}

func TestState_MarshalYAML_HonoursRedactor(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
database:
  dsn: "postgres://user:hunter2@host/db"
`)},
	}
	mgr, err := fastconf.New[secretYAMLCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Without redactor: raw secret leaks.
	raw, err := mgr.Snapshot().MarshalYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Errorf("baseline (nil redactor) should emit raw secret; got:\n%s", raw)
	}

	// With DefaultSecretRedactor: secret replaced, non-secret untouched.
	masked, err := mgr.Snapshot().MarshalYAML(fastconf.DefaultSecretRedactor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(masked), "hunter2") {
		t.Errorf("DefaultSecretRedactor did not mask secret:\n%s", masked)
	}
	if !strings.Contains(string(masked), "REDACTED") {
		t.Errorf("expected REDACTED marker:\n%s", masked)
	}
	if !strings.Contains(string(masked), ":8080") {
		t.Errorf("non-secret field unexpectedly altered:\n%s", masked)
	}

	// Custom redactor: full control over display.
	custom := func(path string, _ any) any { return "[secret:" + path + "]" }
	out, err := mgr.Snapshot().MarshalYAML(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[secret:database.dsn]") {
		t.Errorf("custom redactor did not apply:\n%s", out)
	}
}

func TestState_MarshalYAML_NestedShape(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: x\n  port: 1\n")},
	}
	mgr, _ := fastconf.New[yamlCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	defer mgr.Close()
	out, err := mgr.Snapshot().MarshalYAML(nil)
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
