package confmap

import (
	"encoding/json"
	"testing"
)

func TestPatchPaths(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		want  []string
	}{
		{"simple", `[{"op":"replace","path":"/database/dsn","value":"x"}]`, []string{"database.dsn"}},
		{"top level", `[{"op":"add","path":"/name","value":"x"}]`, []string{"name"}},
		{"array index truncates to slice leaf", `[{"op":"add","path":"/list/0/host","value":"x"}]`, []string{"list"}},
		{"nested map before index kept", `[{"op":"add","path":"/x/y/2/z","value":"x"}]`, []string{"x.y"}},
		{"leading index yields empty", `[{"op":"replace","path":"/0","value":"x"}]`, []string{""}},
		{"escapes", `[{"op":"add","path":"/a~1b/c~0d","value":"x"}]`, []string{"a/b.c~d"}},
		{"root", `[{"op":"replace","path":"","value":{}}]`, []string{""}},
		{"multi", `[{"op":"add","path":"/a","value":1},{"op":"add","path":"/b/c","value":2}]`, []string{"a", "b.c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PatchPaths([]byte(tc.patch))
			if err != nil {
				t.Fatalf("PatchPaths: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("path[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

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
