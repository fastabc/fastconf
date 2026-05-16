package main

import (
	"strings"
	"testing"
)

func TestExportName(t *testing.T) {
	cases := map[string]string{
		"foo":       "Foo",
		"foo_bar":   "FooBar",
		"foo-bar":   "FooBar",
		"http_port": "HTTPPort",
		"db.host":   "DBHost",
		"id":        "ID",
		"":          "Field",
	}
	for in, want := range cases {
		if got := exportName(in); got != want {
			t.Errorf("exportName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEmit_NestedStructAndSlice(t *testing.T) {
	m := map[string]any{
		"http_port": int64(8080),
		"db": map[string]any{
			"host": "localhost",
			"port": int64(5432),
		},
		"tags": []any{"a", "b"},
		"servers": []any{
			map[string]any{"name": "s1", "port": int64(1)},
		},
	}
	var b strings.Builder
	emit(&b, "Config", m)
	out := b.String()
	for _, want := range []string{
		"type Config struct",
		"HTTPPort int64",
		"DB ConfigDB",
		"Tags []string",
		"Servers []ConfigServersItem",
		"type ConfigDB struct",
		"type ConfigServersItem struct",
		"Host string",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}
