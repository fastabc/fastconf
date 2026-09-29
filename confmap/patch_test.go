package confmap

import (
	"encoding/json"
	"strings"
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

func TestApplyPatch_NullContainers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		empty string
		path  string
	}{
		{"map", map[string]any(nil), `{}`, "/value/key"},
		{"slice", []any(nil), `[]`, "/value/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := map[string]any{"value": tc.value}
			if _, err := ApplyPatch(doc, []byte(`[{"op":"test","path":"/value","value":null}]`)); err != nil {
				t.Errorf("nil container must equal JSON null: %v", err)
			}
			if _, err := ApplyPatch(doc, []byte(`[{"op":"test","path":"/value","value":`+tc.empty+`}]`)); err == nil {
				t.Error("nil container must differ from an empty container")
			}
			if _, err := ApplyPatch(doc, []byte(`[{"op":"add","path":"`+tc.path+`","value":1}]`)); err == nil {
				t.Error("cannot add a child to null")
			}
		})
	}
	t.Run("nil root", func(t *testing.T) {
		if _, err := ApplyPatch(nil, []byte(`[{"op":"add","path":"/key","value":1}]`)); err == nil {
			t.Fatal("cannot add a child to null root")
		}
		if got, err := ApplyPatch(nil, []byte(`[{"op":"add","path":"","value":{"key":1}}]`)); err != nil || got["key"] != json.Number("1") {
			t.Fatalf("replacing null root = %v, %v", got, err)
		}
	})
}

func TestApplyPatch_ExactNumericEquality(t *testing.T) {
	large := "1" + strings.Repeat("0", 100)
	for _, tc := range []struct {
		name  string
		value any
		other string
		equal bool
	}{
		{"distinct large integers", json.Number(large), large[:len(large)-1] + "1", false},
		{"distinct precise decimals", json.Number("0." + large), "0." + large[:len(large)-1] + "1", false},
		{"equivalent decimal", json.Number("1.2300"), "123e-2", true},
		{"equivalent huge exponent", json.Number("1e10000000000"), "10e9999999999", true},
		{"different huge exponent", json.Number("1e10000000000"), "1e9999999999", false},
		{"signed zero", json.Number("-0e9999999999"), "0", true},
		{"uint64 precision", uint64(18446744073709551615), "18446744073709551614", false},
		{"float representation", 1.25, "125e-2", true},
		{"numeric string", "1", "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyPatch(map[string]any{"n": tc.value}, []byte(`[{"op":"test","path":"/n","value":`+tc.other+`}]`))
			if (err == nil) != tc.equal {
				t.Fatalf("test %v against %s: err = %v; want equal = %v", tc.value, tc.other, err, tc.equal)
			}
		})
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

// TestApplyPatch_RFC6902Ops pins the in-tree RFC 6902 implementation
// on every op, pointer escapes and array positions.
func TestApplyPatch_RFC6902Ops(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"a":    map[string]any{"b": "x", "c/d": "slash", "e~f": "tilde"},
			"list": []any{"one", "two"},
			"n":    int64(3),
		}
	}
	cases := []struct {
		name, patch, want string
	}{
		{"add object key", `[{"op":"add","path":"/a/new","value":1}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde","new":1},"list":["one","two"],"n":3}`},
		{"add replaces existing key", `[{"op":"add","path":"/a/b","value":"y"}]`, `{"a":{"b":"y","c/d":"slash","e~f":"tilde"},"list":["one","two"],"n":3}`},
		{"add array insert", `[{"op":"add","path":"/list/1","value":"mid"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","mid","two"],"n":3}`},
		{"add array end index", `[{"op":"add","path":"/list/2","value":"end"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two","end"],"n":3}`},
		{"add array dash", `[{"op":"add","path":"/list/-","value":"end"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two","end"],"n":3}`},
		{"escaped keys", `[{"op":"replace","path":"/a/c~1d","value":1},{"op":"remove","path":"/a/e~0f"}]`, `{"a":{"b":"x","c/d":1},"list":["one","two"],"n":3}`},
		{"remove array", `[{"op":"remove","path":"/list/0"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["two"],"n":3}`},
		{"replace array", `[{"op":"replace","path":"/list/1","value":2}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one",2],"n":3}`},
		{"move", `[{"op":"move","from":"/a/b","path":"/moved"}]`, `{"a":{"c/d":"slash","e~f":"tilde"},"list":["one","two"],"moved":"x","n":3}`},
		{"move within array", `[{"op":"move","from":"/list/0","path":"/list/-"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["two","one"],"n":3}`},
		{"move to itself", `[{"op":"move","from":"/a","path":"/a"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two"],"n":3}`},
		{"copy is independent", `[{"op":"copy","from":"/a","path":"/z"},{"op":"remove","path":"/z/b"}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two"],"n":3,"z":{"c/d":"slash","e~f":"tilde"}}`},
		{"test passes numerically", `[{"op":"test","path":"/n","value":3.0},{"op":"test","path":"/list","value":["one","two"]}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two"],"n":3}`},
		{"replace root", `[{"op":"replace","path":"","value":{"only":true}}]`, `{"only":true}`},
		{"null value", `[{"op":"add","path":"/nil","value":null}]`, `{"a":{"b":"x","c/d":"slash","e~f":"tilde"},"list":["one","two"],"n":3,"nil":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyPatch(base(), []byte(tc.patch))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(got)
			if string(b) != tc.want {
				t.Fatalf("got  %s\nwant %s", b, tc.want)
			}
		})
	}
}

func TestApplyPatch_RFC6902Errors(t *testing.T) {
	for _, patch := range []string{
		`not-json`,
		`null`,
		`[{"op":"add","path":null,"value":{}}]`,
		`[{"op":"copy","from":null,"path":"/copy"}]`,
		`[{"op":"add","path":"/bad~2","value":1}]`,
		`[{"op":"add","path":"/bad~","value":1}]`,
		`[{"op":"add","path":"/list/-0","value":1}]`,
		`[{"op":"add","path":"/nil/child","value":1}]`,
		`[{"op":"remove","path":"/nil/child"}]`,
		`[{"op":"replace","path":"/nil/child","value":1}]`,
		`{"op":"add","path":"/x","value":1}`,
		`[{"op":"bogus","path":"/a"}]`,
		`[{"op":"add","value":1}]`,
		`[{"op":"add","path":"/a/b"}]`,
		`[{"op":"add","path":"no-slash","value":1}]`,
		`[{"op":"add","path":"/missing/x","value":1}]`,
		`[{"op":"add","path":"/list/3","value":1}]`,
		`[{"op":"add","path":"/list/01","value":1}]`,
		`[{"op":"remove","path":"/nope"}]`,
		`[{"op":"remove","path":"/list/-"}]`,
		`[{"op":"remove","path":""}]`,
		`[{"op":"replace","path":"/nope","value":1}]`,
		`[{"op":"replace","path":"","value":[1]}]`,
		`[{"op":"move","from":"/a","path":"/a/child"}]`,
		`[{"op":"move","from":"/nope","path":"/x"}]`,
		`[{"op":"copy","from":"/nope","path":"/x"}]`,
		`[{"op":"test","path":"/n","value":4}]`,
		`[{"op":"test","path":"/nope","value":null}]`,
		`[{"op":"add","path":"/n/x","value":1}]`,
	} {
		doc := map[string]any{"a": map[string]any{"b": "x"}, "list": []any{"one", "two"}, "n": int64(3), "nil": nil}
		before, _ := json.Marshal(doc)
		if _, err := ApplyPatch(doc, []byte(patch)); err == nil {
			t.Errorf("%s: expected an error", patch)
		}
		if after, _ := json.Marshal(doc); string(after) != string(before) {
			t.Errorf("%s: failed patch mutated the input: %s", patch, after)
		}
	}
}

// TestApplyPatch_KeepsValueTypes: untouched leaves keep their Go types
// instead of round-tripping through JSON numbers.
func TestApplyPatch_KeepsValueTypes(t *testing.T) {
	got, err := ApplyPatch(map[string]any{"n": int64(3), "f": 1.5}, []byte(`[{"op":"add","path":"/x","value":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["n"].(int64); !ok {
		t.Fatalf("n became %T", got["n"])
	}
	if _, ok := got["f"].(float64); !ok {
		t.Fatalf("f became %T", got["f"])
	}
}
