package merger

import (
	"encoding/json"
	"testing"
)

func TestApplyPatch_AddReplaceRemove(t *testing.T) {
	doc := map[string]any{
		"server":   map[string]any{"addr": ":8080"},
		"features": []any{"a", "b"},
	}
	patch := []byte(`[
		{"op":"replace","path":"/server/addr","value":":9090"},
		{"op":"add","path":"/server/tls","value":true},
		{"op":"remove","path":"/features/0"}
	]`)
	got, err := ApplyPatch(doc, patch)
	if err != nil {
		t.Fatal(err)
	}
	srv := got["server"].(map[string]any)
	if srv["addr"] != ":9090" || srv["tls"] != true {
		t.Errorf("server = %#v", srv)
	}
	feat := got["features"].([]any)
	if len(feat) != 1 || feat[0] != "b" {
		t.Errorf("features = %#v", feat)
	}
}

func TestApplyPatch_InvalidPath(t *testing.T) {
	_, err := ApplyPatch(map[string]any{}, []byte(`[{"op":"remove","path":"/no/such"}]`))
	if err == nil {
		t.Fatal("expected failure on missing path")
	}
}

func TestPatchBytesFromAny(t *testing.T) {
	in := []any{
		map[string]any{"op": "add", "path": "/x", "value": 1},
	}
	out, err := PatchBytesFromAny(in)
	if err != nil {
		t.Fatal(err)
	}
	var rt []map[string]any
	if err := json.Unmarshal(out, &rt); err != nil {
		t.Fatal(err)
	}
	if rt[0]["op"] != "add" {
		t.Errorf("got %#v", rt)
	}
}
