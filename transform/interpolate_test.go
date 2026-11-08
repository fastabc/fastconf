package transform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestInterpolate_EnvDefaults(t *testing.T) {
	root := map[string]any{"dsn": "postgres://${DB_USER:admin}:${DB_PASS}@${DB_HOST:127.0.0.1}:5432/app"}
	env := map[string]string{"DB_USER": "", "DB_PASS": "pw"}
	err := Interpolate(InterpolateOptions{Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok }})(root)
	if err != nil || root["dsn"] != "postgres://admin:pw@127.0.0.1:5432/app" {
		t.Fatalf("%v %v", root, err)
	}
}
func TestInterpolate_Syntax(t *testing.T) {
	env := map[string]string{"A": "${B}", "B": "x"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	for _, tc := range []struct{ in, want string }{
		{"${X:-d}", "d"}, {"$${HOME}", "${HOME}"}, {"${A}", "${B}"}, {"${server.x}", "${server.x}"}, {"${X}", ""},
	} {
		m := map[string]any{"v": []any{tc.in}}
		if err := Interpolate(InterpolateOptions{Lookup: lookup})(m); err != nil {
			t.Fatal(err)
		}
		if got := m["v"].([]any)[0]; got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
	m := map[string]any{"v": "${X:?need X}"}
	if err := Interpolate(InterpolateOptions{Lookup: lookup})(m); !errors.Is(err, errRequired) || !strings.Contains(err.Error(), "interpolate v:") || !strings.Contains(err.Error(), "need X") {
		t.Fatal(err)
	}
}
func TestInterpolate_CrossFieldRef(t *testing.T) {
	root := map[string]any{"server": map[string]any{"base_url": "https://${HOST:example.com}", "port": json.Number("8080")}, "oauth": map[string]any{"callback": "${server.base_url}/cb"}, "probe": "${server.port}", "fallback": "${absent.x:d}"}
	if err := Interpolate(InterpolateOptions{Lookup: func(string) (string, bool) { return "", false }, Refs: true})(root); err != nil {
		t.Fatal(err)
	}
	if root["oauth"].(map[string]any)["callback"] != "https://example.com/cb" || root["probe"] != "8080" || root["fallback"] != "d" {
		t.Fatal(root)
	}
}
func TestInterpolate_RefErrors(t *testing.T) {
	for _, tc := range []struct {
		root map[string]any
		want string
	}{
		{map[string]any{"v": "${a.b}"}, "not found"},
		{map[string]any{"a": map[string]any{"x": "${b.y}"}, "b": map[string]any{"y": "${a.x}"}}, "cycle"},
		{map[string]any{"v": "${server.tls}", "server": map[string]any{"tls": map[string]any{}}}, "not a scalar"},
	} {
		err := Interpolate(InterpolateOptions{Refs: true})(tc.root)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v", err)
		}
	}
}
func TestInterpolate_EnvValueNotReexpanded(t *testing.T) {
	for range 20 {
		root := map[string]any{"a": map[string]any{"x": "${A}"}, "b": "${a.x}"}
		lookup := func(k string) (string, bool) {
			if k == "A" {
				return "${B}", true
			}
			return "x", true
		}
		if err := Interpolate(InterpolateOptions{Refs: true, Lookup: lookup})(root); err != nil {
			t.Fatal(err)
		}
		if root["b"] != "${B}" {
			t.Fatal(root)
		}
	}
}
