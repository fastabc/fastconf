package fastconf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/providers/source"
)

type scalarJSON string

func (s scalarJSON) MarshalJSON() ([]byte, error) { return []byte(s), nil }

func TestDiff_ScalarJSONEquivalence(t *testing.T) {
	values := []any{nil, false, true, "", "x", "<>&\u2028", "\xff", "\xfe", "\ufffd",
		json.Number("1"), json.Number("1.0"), json.Number("1e0"), json.Number("0"), json.Number("-0"),
		json.Number(""), json.Number("bad"), json.Number("other"), json.Number("null"), json.Number("true"),
		json.Number("01"), json.Number("1e999"), 1, 1.0, math.NaN(), math.Inf(1),
		[]any{1}, []any{}, []any(nil), map[string]any(nil), scalarJSON("true"), scalarJSON("null"), scalarJSON("1"),
	}
	for i, a := range values {
		for j, b := range values {
			ja, _ := json.Marshal(a)
			jb, _ := json.Marshal(b)
			want := string(ja) == string(jb)
			if got := jsonEqual(a, b); got != want {
				t.Errorf("pair %d/%d (%#v/%#v): equal=%v want=%v", i, j, a, b, got, want)
			}
			diff := diffMaps("", map[string]any{"leaf": a}, map[string]any{"leaf": b})
			if (len(diff) == 0) != want {
				t.Errorf("pair %d/%d: diff=%v want equal=%v", i, j, diff, want)
			}
		}
	}
}

func TestState_Diff(t *testing.T) {
	a, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: alpha\ndb:\n  dsn: x\n  pool: 5\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("b", "yaml", []byte("name: beta\ndb:\n  dsn: x\n  pool: 9\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	out := a.Snapshot().Diff(b.Snapshot())
	if len(out) == 0 {
		t.Fatal("expected diffs")
	}
	var nameSeen, poolSeen bool
	for _, e := range out {
		if e.Change != DiffModified {
			continue
		}
		switch e.Path {
		case "name":
			if fmt.Sprintf("%v -> %v", e.Before, e.After) == "alpha -> beta" {
				nameSeen = true
			}
		case "db.pool":
			// JSON round-trip turns numeric scalars into float64; compare
			// via Sprintf to keep the assertion typed-display-agnostic.
			if fmt.Sprintf("%v -> %v", e.Before, e.After) == "5 -> 9" {
				poolSeen = true
			}
		}
	}
	if !nameSeen || !poolSeen {
		t.Fatalf("missing diffs: %+v", out)
	}
	// FormatDiff round-trips the structured entries back to human-readable
	// lines so existing log scrapers keep working.
	lines := FormatDiff(out)
	if len(lines) != len(out) {
		t.Errorf("FormatDiff lost entries: %d → %d", len(out), len(lines))
	}
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

func TestProvenance_FullExplain(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("base", "yaml", []byte("name: from-base\ndb:\n  dsn: base-dsn\n  pool: 4\n"))),
		WithProvider(source.NewBytes("override", "yaml", []byte("name: from-override\ndb:\n  pool: 16\n"))),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	snap := mgr.Snapshot()
	chain := snap.Explain("name")
	if len(chain) < 2 {
		t.Fatalf("name chain=%d want >=2", len(chain))
	}
	winner := chain[len(chain)-1]
	if winner.Source.Path != "provider://override" {
		t.Fatalf("name winner=%s want provider://override", winner.Source.Path)
	}
	if got := snap.Explain("db.dsn"); len(got) != 1 || got[0].Source.Path != "provider://base" {
		t.Fatalf("db.dsn chain wrong: %+v", got)
	}
	got := snap.Explain("name")
	got[len(got)-1].Source.Path = "caller-mutation"
	if again := snap.Explain("name"); again[len(again)-1].Source.Path == "caller-mutation" {
		t.Fatal("Explain returned the snapshot's own provenance records")
	}
}

func TestProvenance_OffByDefault(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: x\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Snapshot().Explain("name"); got != nil {
		t.Fatalf("expected no provenance by default, got %+v", got)
	}
}

func TestExplain_PerLayerValues(t *testing.T) {
	mgr, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("k: 1\n"))),
		WithProvider(source.NewBytes("over", "yaml", []byte("k: 2\n"))),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	chain := mgr.Snapshot().Explain("k")
	if len(chain) < 2 {
		t.Fatalf("want >=2 layers, got %d", len(chain))
	}
	last := chain[len(chain)-1]
	if v, ok := last.Value.(int); !ok || v != 2 {
		switch x := last.Value.(type) {
		case int:
			if x != 2 {
				t.Fatalf("winner value=%v", x)
			}
		case float64:
			if x != 2 {
				t.Fatalf("winner value=%v", x)
			}
		default:
			t.Fatalf("winner value type=%T value=%v", last.Value, last.Value)
		}
	}
}
