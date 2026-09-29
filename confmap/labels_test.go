package confmap_test

import (
	"testing"

	"github.com/fastabc/fastconf/confmap"
)

func TestExpandLabels_StringSlice(t *testing.T) {
	in := []string{
		"traefik.http.services.dummy-svc.loadbalancer.server.port=9999",
		"traefik.enable=true",
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{})
	port, ok := confmap.GetDotted(out, "traefik.http.services.dummy-svc.loadbalancer.server.port")
	if !ok || port != "9999" {
		t.Fatalf("port got %v ok=%v want \"9999\"", port, ok)
	}
	if v, _ := confmap.GetDotted(out, "traefik.enable"); v != "true" {
		t.Fatalf("enable got %v want \"true\" (no coerce by default)", v)
	}
}

func TestExpandLabels_AnySlice(t *testing.T) {
	in := []any{
		"a.b.c=1",
		"a.b.d=2",
		42, // non-string entries silently skipped
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{})
	if v, _ := confmap.GetDotted(out, "a.b.c"); v != "1" {
		t.Fatalf("got %v", v)
	}
	if v, _ := confmap.GetDotted(out, "a.b.d"); v != "2" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_MapStringString(t *testing.T) {
	in := map[string]string{
		"k8s.io/component":    "frontend",
		"app.kubernetes/name": "web",
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{Separator: "/"})
	if v, _ := confmap.Get(out, "k8s.io", "component"); v != "frontend" {
		t.Fatalf("component got %v", v)
	}
	if v, _ := confmap.Get(out, "app.kubernetes", "name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
}

func TestExpandLabels_PrefixFilterAndStrip(t *testing.T) {
	in := []string{
		"traefik.enable=true",
		"traefik.http.routers.api=Host(`api`)",
		"unrelated.foo=bar", // filtered out
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{
		Prefix:      "traefik.",
		StripPrefix: true,
	})
	if v, _ := confmap.Get(out, "enable"); v != "true" {
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
	out := confmap.ExpandLabels(in, confmap.LabelOptions{Coerce: true})
	if v, _ := confmap.GetDotted(out, "a.bool"); v != true {
		t.Fatalf("bool got %v (%T)", v, v)
	}
	if v, _ := confmap.GetDotted(out, "a.int"); v != int64(42) {
		t.Fatalf("int got %v (%T)", v, v)
	}
	if v, _ := confmap.GetDotted(out, "a.float"); v != 3.14 {
		t.Fatalf("float got %v (%T)", v, v)
	}
	if v, _ := confmap.GetDotted(out, "a.str"); v != "hello" {
		t.Fatalf("str got %v (%T)", v, v)
	}
}

func TestExpandLabels_DropsMalformed(t *testing.T) {
	in := []string{
		"valid.key=value",
		"no_equals_sign",
		"=empty-key",
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{})
	if got := len(out); got != 1 {
		t.Fatalf("expected only 1 top-level key, got %d (%v)", got, out)
	}
	if v, _ := confmap.GetDotted(out, "valid.key"); v != "value" {
		t.Fatalf("got %v", v)
	}
}

func TestExpandLabels_ValueContainsEquals(t *testing.T) {
	in := []string{"traefik.http.routers.api.rule=Host(`a.com`)&&PathPrefix(`/x`)"}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{})
	rule, _ := confmap.GetDotted(out, "traefik.http.routers.api.rule")
	if rule != "Host(`a.com`)&&PathPrefix(`/x`)" {
		t.Fatalf("rule lost trailing '=' segments: got %q", rule)
	}
}

// Multi-separator: K8s recommended labels carry both "/" (prefix/name)
// and "." (within the prefix). With Separators={"/", "."} the keys
// decompose into a coherent ["app","kubernetes","io","name"] path so
// downstream lookups can use GetDotted naturally.
func TestExpandLabels_MultiSeparator_K8s(t *testing.T) {
	in := map[string]string{
		"app.kubernetes.io/name":      "web",
		"app.kubernetes.io/component": "frontend",
		"app.kubernetes.io/version":   "1.2.3",
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{
		Separators: []string{"/", "."},
	})
	if v, _ := confmap.GetDotted(out, "app.kubernetes.io.name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
	if v, _ := confmap.GetDotted(out, "app.kubernetes.io.component"); v != "frontend" {
		t.Fatalf("component got %v", v)
	}
	if v, _ := confmap.GetDotted(out, "app.kubernetes.io.version"); v != "1.2.3" {
		t.Fatalf("version got %v", v)
	}
}

// Separators takes precedence over the singular Separator when both
// are set.
func TestExpandLabels_SeparatorsBeatsSeparator(t *testing.T) {
	in := []string{"a/b.c=v"}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{
		Separator:  "X", // ignored
		Separators: []string{"/", "."},
	})
	if v, _ := confmap.Get(out, "a", "b", "c"); v != "v" {
		t.Fatalf("got %v", v)
	}
}

// Multi-separator + prefix strip: leading separator left by prefix
// removal is also trimmed so "traefik.http.x" with Prefix="traefik"
// produces ["http","x"] rather than ["", "http", "x"].
func TestExpandLabels_MultiSeparator_PrefixStrip(t *testing.T) {
	in := []string{
		"my.app/config.timeout=30s",
		"my.app/config.retries=3",
	}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{
		Prefix:      "my.app",
		StripPrefix: true,
		Separators:  []string{"/", "."},
	})
	if v, _ := confmap.GetDotted(out, "config.timeout"); v != "30s" {
		t.Fatalf("timeout got %v", v)
	}
	if v, _ := confmap.GetDotted(out, "config.retries"); v != "3" {
		t.Fatalf("retries got %v", v)
	}
}

// Single-separator fallback path still works.
func TestExpandLabels_SeparatorFallback(t *testing.T) {
	in := []string{"a.b.c=v"}
	out := confmap.ExpandLabels(in, confmap.LabelOptions{Separator: "."})
	if v, _ := confmap.Get(out, "a", "b", "c"); v != "v" {
		t.Fatalf("got %v", v)
	}
}
