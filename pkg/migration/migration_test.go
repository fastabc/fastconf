package migration

import "testing"

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
