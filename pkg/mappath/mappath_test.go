package mappath_test

import (
	"testing"

	"github.com/fastabc/fastconf/pkg/mappath"
)

func TestSet_CreatesIntermediateMaps(t *testing.T) {
	root := map[string]any{}
	mappath.Set(root, []string{"a", "b", "c"}, 42)
	if got, _ := mappath.Get(root, "a", "b", "c"); got != 42 {
		t.Fatalf("got %v want 42", got)
	}
}

func TestSet_OverwritesNonMapIntermediate(t *testing.T) {
	root := map[string]any{"a": "leaf"}
	mappath.Set(root, []string{"a", "b"}, 1)
	if _, ok := root["a"].(map[string]any); !ok {
		t.Fatalf("expected a to become map, got %T", root["a"])
	}
}

func TestGet_MissingReturnsFalse(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": 1}}
	if _, ok := mappath.Get(root, "a", "x"); ok {
		t.Fatal("want missing")
	}
}

func TestDelete_RemovesLeaf(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": 1}}
	mappath.Delete(root, []string{"a", "b"})
	if _, ok := mappath.Get(root, "a", "b"); ok {
		t.Fatal("want deleted")
	}
}

func TestGet_NonMapIntermediateReturnsFalse(t *testing.T) {
	root := map[string]any{"a": "leaf"}
	if _, ok := mappath.Get(root, "a", "b"); ok {
		t.Fatal("want false for non-map intermediate")
	}
}

func TestDelete_IgnoresMissingIntermediate(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": 1}}
	mappath.Delete(root, []string{"x", "y"})
	if got, ok := mappath.Get(root, "a", "b"); !ok || got != 1 {
		t.Fatalf("unexpected value after delete: got %v ok=%v", got, ok)
	}
}

func TestExpandLabels_StringSlice(t *testing.T) {
	in := []string{
		"traefik.http.services.dummy-svc.loadbalancer.server.port=9999",
		"traefik.enable=true",
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{})
	port, ok := mappath.GetDotted(out, "traefik.http.services.dummy-svc.loadbalancer.server.port")
	if !ok || port != "9999" {
		t.Fatalf("port got %v ok=%v want \"9999\"", port, ok)
	}
	if v, _ := mappath.GetDotted(out, "traefik.enable"); v != "true" {
		t.Fatalf("enable got %v want \"true\" (no coerce by default)", v)
	}
}

func TestExpandLabels_AnySlice(t *testing.T) {
	in := []any{
		"a.b.c=1",
		"a.b.d=2",
		42, // non-string entries silently skipped
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{})
	if v, _ := mappath.GetDotted(out, "a.b.c"); v != "1" {
		t.Fatalf("got %v", v)
	}
	if v, _ := mappath.GetDotted(out, "a.b.d"); v != "2" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_MapStringString(t *testing.T) {
	in := map[string]string{
		"k8s.io/component":   "frontend",
		"app.kubernetes/name": "web",
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{Separator: "/"})
	if v, _ := mappath.Get(out, "k8s.io", "component"); v != "frontend" {
		t.Fatalf("component got %v", v)
	}
	if v, _ := mappath.Get(out, "app.kubernetes", "name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
}

func TestExpandLabels_PrefixFilterAndStrip(t *testing.T) {
	in := []string{
		"traefik.enable=true",
		"traefik.http.routers.api=Host(`api`)",
		"unrelated.foo=bar", // filtered out
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{
		Prefix:      "traefik.",
		StripPrefix: true,
	})
	if v, _ := mappath.Get(out, "enable"); v != "true" {
		t.Fatalf("enable got %v", v)
	}
	if _, ok := out["unrelated"]; ok {
		t.Fatalf("unrelated keys should have been filtered")
	}
}

func TestExpandLabels_Coerce(t *testing.T) {
	in := []string{
		"a.bool=true",
		"a.int=42",
		"a.float=3.14",
		"a.str=hello",
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{Coerce: true})
	if v, _ := mappath.GetDotted(out, "a.bool"); v != true {
		t.Fatalf("bool got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(out, "a.int"); v != int64(42) {
		t.Fatalf("int got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(out, "a.float"); v != 3.14 {
		t.Fatalf("float got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(out, "a.str"); v != "hello" {
		t.Fatalf("str got %v (%T)", v, v)
	}
}

func TestExpandLabels_DropsMalformed(t *testing.T) {
	in := []string{
		"valid.key=value",
		"no_equals_sign",
		"=empty-key",
	}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{})
	if got := len(out); got != 1 {
		t.Fatalf("expected only 1 top-level key, got %d (%v)", got, out)
	}
	if v, _ := mappath.GetDotted(out, "valid.key"); v != "value" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_ValueContainsEquals(t *testing.T) {
	in := []string{"traefik.http.routers.api.rule=Host(`a.com`)&&PathPrefix(`/x`)"}
	out := mappath.ExpandLabels(in, mappath.LabelOptions{})
	rule, _ := mappath.GetDotted(out, "traefik.http.routers.api.rule")
	if rule != "Host(`a.com`)&&PathPrefix(`/x`)" {
		t.Fatalf("rule lost trailing '=' segments: got %q", rule)
	}
}
