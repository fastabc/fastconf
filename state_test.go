package fastconf

// P2.4: every boundary method on *State[T] must tolerate a nil receiver
// the same way Introspect / Dump / Sub already do. This file pins that
// contract down so future additions cannot silently regress.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

type nilCfg struct {
	Name string `json:"name"`
}

func TestState_NilSafety(t *testing.T) {
	var s *State[nilCfg] // intentionally nil

	t.Run("Redacted", func(t *testing.T) {
		if got := s.Redacted(); got != nil {
			t.Errorf("Redacted on nil: want nil, got %v", got)
		}
	})

	t.Run("Redact", func(t *testing.T) {
		if got := s.Redact(DefaultSecretRedactor); got != nil {
			t.Errorf("Redact on nil: want nil, got %v", got)
		}
	})

	t.Run("FeatureRules", func(t *testing.T) {
		if got := s.FeatureRules(); got != nil {
			t.Errorf("FeatureRules on nil: want nil, got %v", got)
		}
	})

	t.Run("Origins", func(t *testing.T) {
		if got := s.Origins(); got != nil {
			t.Errorf("Origins on nil: want nil, got %v", got)
		}
	})

	t.Run("Explain", func(t *testing.T) {
		if got := s.Explain("any.path"); got != nil {
			t.Errorf("Explain on nil: want nil, got %v", got)
		}
	})

	t.Run("Lookup", func(t *testing.T) {
		if got := s.Lookup("any.path"); got != nil {
			t.Errorf("Lookup on nil: want nil, got %v", got)
		}
	})

	t.Run("LookupStrict", func(t *testing.T) {
		got, err := s.LookupStrict("any.path")
		if got != nil {
			t.Errorf("LookupStrict on nil: want nil slice, got %v", got)
		}
		if !errors.Is(err, ErrNoOrigin) {
			t.Errorf("LookupStrict on nil: want ErrNoOrigin, got %v", err)
		}
	})

	t.Run("Diff", func(t *testing.T) {
		// nil vs nil → no differences
		if got := s.Diff(nil); len(got) != 0 {
			t.Errorf("nil.Diff(nil): want empty, got %v", got)
		}
		// nil vs nil receiver on either side must not panic
		other := s
		_ = other.Diff(s)
	})

	t.Run("Introspect", func(t *testing.T) {
		// Introspect on a nil State returns a nil *Introspection
		// (documented behaviour). Calling Keys/Settings/At on that nil
		// holder must NOT panic and must return empty.
		ins := s.Introspect()
		if got := ins.Keys(); len(got) != 0 {
			t.Errorf("Introspect.Keys on nil state: want empty, got %v", got)
		}
		if got := ins.Settings(); len(got) != 0 {
			t.Errorf("Introspect.Settings on nil state: want empty, got %v", got)
		}
		if got := ins.At("foo"); len(got) != 0 {
			t.Errorf("Introspect.At on nil state: want empty, got %v", got)
		}
	})

	t.Run("Dump", func(t *testing.T) {
		b, err := s.Dump(DumpYAML, nil)
		if err != nil {
			t.Fatalf("Dump on nil: unexpected error %v", err)
		}
		if string(b) != "{}\n" {
			t.Errorf("Dump on nil: want \"{}\\n\", got %q", b)
		}
		// Same with a redactor — must not panic.
		if _, err := s.Dump(DumpYAML, DefaultSecretRedactor); err != nil {
			t.Errorf("Dump(redactor) on nil: unexpected error %v", err)
		}
	})
}

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
	defer mgr.Close()
	state := mgr.Snapshot()

	// First call.
	a, err := state.Dump(DumpYAML, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Second call must produce byte-identical output (deterministic order).
	b, err := state.Dump(DumpYAML, nil)
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

func TestState_Dump_NilState(t *testing.T) {
	var s *State[yamlCfg]
	b, err := s.Dump(DumpYAML, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}\n" {
		t.Errorf("nil state should dump to empty map, got %q", b)
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
	defer mgr.Close()

	// Without redactor: raw secret leaks.
	raw, err := mgr.Snapshot().Dump(DumpYAML, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Errorf("baseline (nil redactor) should emit raw secret; got:\n%s", raw)
	}

	// With DefaultSecretRedactor: secret replaced, non-secret untouched.
	masked, err := mgr.Snapshot().Dump(DumpYAML, DefaultSecretRedactor)
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
	out, err := mgr.Snapshot().Dump(DumpYAML, custom)
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
	defer mgr.Close()
	out, err := mgr.Snapshot().Dump(DumpYAML, nil)
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

// TestState_Dump_JSONParity verifies SPEC-A2 acceptance: Dump(DumpJSON,
// nil) round-trips to the same tree as json.Marshal(*state.Value) does
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
	defer mgr.Close()
	snap := mgr.Snapshot()

	dump, err := snap.Dump(DumpJSON, nil)
	if err != nil {
		t.Fatalf("Dump(JSON): %v", err)
	}
	direct, err := json.Marshal(snap.Value)
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
	defer mgr.Close()
	out, err := mgr.Snapshot().Dump(DumpTOML, nil)
	if err != nil {
		t.Fatalf("Dump(TOML): %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "[server]") || !strings.Contains(s, "[database]") {
		t.Errorf("expected TOML section headers in:\n%s", s)
	}
}

// mapsEqualJSON compares two JSON-decoded maps by re-marshalling to a
// stable form. Sufficient for parity tests where ordering is the only
// expected variance.
func mapsEqualJSON(a, b map[string]any) bool {
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)
	return bytes.Equal(pa, pb)
}

func TestSourcePriorityBand(t *testing.T) {
	tests := []struct {
		name string
		ref  SourceRef
		want string
	}{
		{
			name: "file base",
			ref:  SourceRef{Priority: 1004, Path: "conf.d/base/00.yaml"},
			want: "file:base",
		},
		{
			name: "file overlay single profile",
			ref:  SourceRef{Priority: 2001, Profile: "prod"},
			want: "file:overlay:prod",
		},
		{
			name: "file overlay multi-axis",
			ref:  SourceRef{Priority: 3010, Profile: "region:us-east-1"},
			want: "file:overlay:region:us-east-1",
		},
		{
			name: "generator default priority (PriorityGenerator=70)",
			ref:  SourceRef{Priority: 7070, Path: "gen://flags/feature.yaml"},
			want: "generator:flags/feature.yaml",
		},
		{
			name: "generator custom RawLayer.Priority",
			ref:  SourceRef{Priority: 7042, Path: "gen://buildinfo/info.yaml"},
			want: "generator:buildinfo/info.yaml",
		},
		{
			name: "provider CLI band",
			ref:  SourceRef{Priority: 8060, Path: "provider://cli"},
			want: "provider:cli",
		},
		{
			name: "unknown",
			ref:  SourceRef{Priority: 17, Path: "anywhere"},
			want: "unknown:17",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SourcePriorityBand(tt.ref); got != tt.want {
				t.Fatalf("SourcePriorityBand(%+v) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}
