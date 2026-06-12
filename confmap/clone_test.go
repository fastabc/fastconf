package confmap

import (
	"reflect"
	"testing"
)

func TestDeepClone_Independence(t *testing.T) {
	orig := map[string]any{
		"scalar": "s",
		"nested": map[string]any{"k": 1},
		"list":   []any{map[string]any{"id": "a"}, "x"},
	}
	got := DeepClone(orig)
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("clone differs: %v vs %v", got, orig)
	}
	got["nested"].(map[string]any)["k"] = 2
	got["list"].([]any)[0].(map[string]any)["id"] = "b"
	got["list"].([]any)[1] = "y"
	if orig["nested"].(map[string]any)["k"] != 1 {
		t.Errorf("nested map aliased into clone")
	}
	if orig["list"].([]any)[0].(map[string]any)["id"] != "a" {
		t.Errorf("slice element map aliased into clone")
	}
	if orig["list"].([]any)[1] != "x" {
		t.Errorf("slice aliased into clone")
	}
}

func TestDeepClone_Nil(t *testing.T) {
	if DeepClone(nil) != nil {
		t.Errorf("nil input must yield nil")
	}
}
