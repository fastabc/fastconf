package transform

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/fastabc/fastconf/codec"
)

func TestChainRun(t *testing.T) {
	c, err := New(2,
		Migration{From: 0, To: 1, Apply: func(m map[string]any) error {
			m["b"] = m["a"]
			delete(m, "a")
			return nil
		}},
		Migration{From: 1, To: 2, Apply: func(m map[string]any) error {
			m["c"] = "two"
			return nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"a": "one"}
	v, err := c.Run(m)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Fatalf("v=%d want 2", v)
	}
	if m["b"] != "one" || m["c"] != "two" {
		t.Fatalf("bad migration: %+v", m)
	}
	if CurrentVersion(m) != 2 {
		t.Fatalf("schemaVersion not stamped: %+v", m)
	}
}

func TestChainGap(t *testing.T) {
	c, _ := New(3,
		Migration{From: 0, To: 1, Apply: func(map[string]any) error { return nil }},
	)
	m := map[string]any{}
	if _, err := c.Run(m); err == nil {
		t.Fatal("expected gap error")
	}
}

func TestChainNoOp(t *testing.T) {
	c, _ := New(1, Migration{From: 0, To: 1, Apply: func(map[string]any) error { return nil }})
	m := map[string]any{"_meta": map[string]any{"schemaVersion": 1}}
	v, err := c.Run(m)
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func TestCurrentVersion_JSONCodec(t *testing.T) {
	m, err := codec.DecodeAny("json", []byte(`{"_meta":{"schemaVersion":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	tree := m.(map[string]any)
	if v := CurrentVersion(tree); v != 2 {
		t.Fatalf("CurrentVersion = %d, want 2", v)
	}
	c, err := New(2, Migration{From: 1, To: 2, Apply: func(map[string]any) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Run(tree); err != nil || v != 2 {
		t.Fatalf("Run = %d, %v", v, err)
	}
}

func TestCurrentVersion_RejectsNonIntegers(t *testing.T) {
	for _, raw := range []any{json.Number("1.5"), json.Number("99999999999999999999"), 1.5, "2", uint64(math.MaxUint64)} {
		if v := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); v != 0 {
			t.Errorf("%#v -> %d, want 0", raw, v)
		}
	}
	for _, raw := range []any{3, int64(3), float64(3), json.Number("3"), uint64(3)} {
		if v := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); v != 3 {
			t.Errorf("%#v -> %d, want 3", raw, v)
		}
	}
}
