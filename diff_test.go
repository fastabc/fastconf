package fastconf

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

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
