package merger

import "testing"

func TestDeep_AddNewKey(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"b": 2}
	if err := Deep(dst, src, Options{}); err != nil {
		t.Fatal(err)
	}
	if dst["a"] != 1 || dst["b"] != 2 {
		t.Errorf("got %v", dst)
	}
}

func TestDeep_OverwriteScalar(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"a": 2}
	_ = Deep(dst, src, Options{})
	if dst["a"] != 2 {
		t.Errorf("want 2, got %v", dst["a"])
	}
}

func TestDeep_RecursiveMap(t *testing.T) {
	dst := map[string]any{"db": map[string]any{"host": "x", "port": 1}}
	src := map[string]any{"db": map[string]any{"port": 2}}
	_ = Deep(dst, src, Options{})
	got := dst["db"].(map[string]any)
	if got["host"] != "x" || got["port"] != 2 {
		t.Errorf("merged wrong: %v", got)
	}
}

func TestDeep_SliceReplaceByDefault(t *testing.T) {
	dst := map[string]any{"l": []any{1, 2}}
	src := map[string]any{"l": []any{3}}
	_ = Deep(dst, src, Options{})
	got := dst["l"].([]any)
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("slice should be replaced: %v", got)
	}
}

func TestDeep_SliceAppend(t *testing.T) {
	dst := map[string]any{"l": []any{1, 2}}
	src := map[string]any{"l": []any{3}}
	_ = Deep(dst, src, Options{AppendSlices: true})
	got := dst["l"].([]any)
	if len(got) != 3 {
		t.Errorf("slice should append: %v", got)
	}
}

func TestDeep_StrictTypeMismatch(t *testing.T) {
	dst := map[string]any{"a": map[string]any{"x": 1}}
	src := map[string]any{"a": "not-a-map"}
	if err := Deep(dst, src, Options{Strict: true}); err == nil {
		t.Error("expected strict error")
	}
}
