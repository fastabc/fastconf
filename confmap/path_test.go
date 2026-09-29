package confmap_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/confmap"
)

func TestPath_SetGetDelete(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial any
	}{
		{"missing parent", nil}, {"nil map", map[string]any(nil)}, {"scalar parent", "leaf"}, {"map parent", map[string]any{"other": 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := map[string]any{"keep": true}
			if tc.initial != nil {
				root["server"] = tc.initial
			}
			confmap.Set(root, []string{"server", "port"}, 80)
			if got, ok := confmap.Get(root, "server", "port"); !ok || got != 80 {
				t.Fatalf("Get = %v, %v", got, ok)
			}
			if got, ok := confmap.GetDotted(root, "server.port"); !ok || got != 80 {
				t.Fatalf("GetDotted = %v, %v", got, ok)
			}
			confmap.SetDotted(root, "server.port", 90)
			if got, ok := confmap.Get(root, "server", "port"); !ok || got != 90 {
				t.Fatalf("SetDotted result = %v, %v", got, ok)
			}
			confmap.Delete(root, []string{"server", "port"})
			confmap.SetDotted(root, "server.port", 100)
			confmap.DeleteDotted(root, "server.port")
			want := map[string]any{"server": map[string]any{}, "keep": true}
			if tc.name == "map parent" {
				want["server"] = map[string]any{"other": 9}
			}
			if !reflect.DeepEqual(root, want) {
				t.Fatalf("after delete = %#v; want %#v", root, want)
			}
		})
	}
}

func TestPath_MissingAndEmpty(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": 1}, "leaf": "value"}
	for _, path := range []string{"", "missing.child", "a.missing", "leaf.child"} {
		if got, ok := confmap.GetDotted(root, path); ok || got != nil {
			t.Fatalf("GetDotted(%q) = %v, %v", path, got, ok)
		}
		before := confmap.DeepClone(root)
		confmap.DeleteDotted(root, path)
		if !reflect.DeepEqual(root, before) {
			t.Fatalf("DeleteDotted(%q) changed other keys", path)
		}
	}
	confmap.Set(root, nil, 1)
	confmap.SetDotted(nil, "a.b", 1)
	confmap.DeleteDotted(nil, "a.b")
	if got, ok := confmap.GetDotted(nil, "a.b"); got != nil || ok {
		t.Fatalf("nil Get = %v, %v", got, ok)
	}
	if parts := confmap.Split(""); len(parts) != 0 {
		t.Fatalf("Split empty = %v", parts)
	}
	if parts := confmap.Split("a.b"); !reflect.DeepEqual(parts, []string{"a", "b"}) {
		t.Fatalf("Split = %v", parts)
	}
}
