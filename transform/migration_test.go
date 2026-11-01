package transform

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/fastabc/fastconf/codec"
)

func TestChainRun(t *testing.T) {
	c, err := NewChain(2,
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
	c, _ := NewChain(3,
		Migration{From: 0, To: 1, Apply: func(map[string]any) error { return nil }},
	)
	m := map[string]any{}
	if _, err := c.Run(m); err == nil {
		t.Fatal("expected gap error")
	}
}

func TestChainNoOp(t *testing.T) {
	c, _ := NewChain(1, Migration{From: 0, To: 1, Apply: func(map[string]any) error { return nil }})
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
	c, err := NewChain(2, Migration{From: 1, To: 2, Apply: func(map[string]any) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Run(tree); err != nil || v != 2 {
		t.Fatalf("Run = %d, %v", v, err)
	}
}

func TestCurrentVersion_RejectsNonIntegers(t *testing.T) {
	for _, raw := range []any{json.Number("1.5"), json.Number("99999999999999999999"), 1.5, float32(1.5), math.NaN(), math.Inf(1), math.Inf(-1), uint64(math.MaxUint64), uint(math.MaxUint)} {
		if v := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); v != 0 {
			t.Errorf("%#v -> %d, want 0", raw, v)
		}
	}
	for _, raw := range []any{3, int8(3), int16(3), int32(3), int64(3), uint(3), uint8(3), uint16(3), uint32(3), uint64(3), float32(3), float64(3), json.Number("3"), "3"} {
		if v := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); v != 3 {
			t.Errorf("%#v -> %d, want 3", raw, v)
		}
	}
}

func TestCurrentVersion_StringVersions(t *testing.T) {
	for raw, want := range map[string]int{"2": 2, " 3 ": 3, "1.5": 0, "abc": 0, "99999999999999999999": 0} {
		if got := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); got != want {
			t.Errorf("%q: got %d want %d", raw, got, want)
		}
	}
}

func TestCurrentVersion_IntBoundaries(t *testing.T) {
	for _, raw := range []any{math.MaxInt, int64(math.MaxInt), uint64(math.MaxInt), strconv.Itoa(math.MaxInt), json.Number(strconv.Itoa(math.MaxInt))} {
		if got := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); got != math.MaxInt {
			t.Errorf("%T(%v): got %d want MaxInt", raw, raw, got)
		}
	}
	for _, raw := range []any{float64(math.MaxInt) + 1, uint64(math.MaxInt) + 1} {
		if got := CurrentVersion(map[string]any{MetaKey: map[string]any{FieldKey: raw}}); got != 0 {
			t.Errorf("out of range %T(%v): got %d", raw, raw, got)
		}
	}
}

func TestChain_INIStringVersionSkipsAppliedMigrations(t *testing.T) {
	decoder, ok := codec.Lookup("ini")
	if !ok {
		t.Fatal("INI decoder is not registered")
	}
	decoded, err := decoder.Decode([]byte("[_meta]\nschemaVersion = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	chain, err := NewChain(2, Migration{From: 0, To: 2, Apply: func(map[string]any) error {
		t.Error("already applied migration ran again")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := chain.Run(decoded); err != nil || got != 2 {
		t.Fatalf("version=%d err=%v", got, err)
	}
}
