package confmap

import (
	"encoding/json"
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

func TestDeepClone_PreservesNullAndEmptyContainers(t *testing.T) {
	orig := map[string]any{
		"nil_map": map[string]any(nil), "empty_map": map[string]any{},
		"nil_slice": []any(nil), "empty_slice": []any{},
	}
	got := DeepClone(orig)
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("clone = %#v; want %#v", got, orig)
	}
	before, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("clone changed JSON: %s -> %s", before, after)
	}
}

func TestCloneValue_DetachesNestedContainers(t *testing.T) {
	orig := []any{map[string]any{"k": []any{"a"}}}
	got := CloneValue(orig).([]any)
	got[0].(map[string]any)["k"].([]any)[0] = "b"
	if orig[0].(map[string]any)["k"].([]any)[0] != "a" {
		t.Fatal("CloneValue aliased a nested container")
	}
	if CloneValue("s") != "s" || CloneValue(nil) != nil {
		t.Fatal("CloneValue must return scalars unchanged")
	}
}
