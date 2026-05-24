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

func TestEmit_DeduplicatesCollidingFieldNames(t *testing.T) {
	m := map[string]any{
		"foo-bar": int64(1),
		"foo_bar": int64(2),
	}
	var b strings.Builder
	emit(&b, "Config", m)
	out := b.String()
	for _, want := range []string{
		"FooBar int64 `yaml:\"foo-bar\" json:\"foo-bar\"`",
		"FooBar2 int64 `yaml:\"foo_bar\" json:\"foo_bar\"`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestEmit_DeduplicatesNestedTypeNames(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{"x": int64(1)},
		},
		"a_b": map[string]any{"y": int64(2)},
	}
	var b strings.Builder
	emit(&b, "Config", m)
	out := b.String()
	for _, want := range []string{
		"AB ConfigAB",
		"B ConfigAB2",
		"type ConfigAB struct",
		"type ConfigAB2 struct",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestEmit_SliceSamplesMergeObjectFields(t *testing.T) {
	m := map[string]any{
		"servers": []any{
			map[string]any{"name": "api"},
			map[string]any{"port": int64(8080), "tls": true},
		},
	}
	var b strings.Builder
	emit(&b, "Config", m)
	out := b.String()
	for _, want := range []string{
		"Servers []ConfigServersItem",
		"Name string",
		"Port int64",
		"TLS bool",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestEmit_SliceSamplesKeepConflictingFieldsAsAny(t *testing.T) {
	m := map[string]any{
		"items": []any{
			map[string]any{"value": "one"},
			map[string]any{"value": int64(2)},
			map[string]any{"value": "three"},
		},
	}
	var b strings.Builder
	emit(&b, "Config", m)
	out := b.String()
	if !strings.Contains(out, "Value any") {
		t.Fatalf("conflicting field should be any:\n%s", out)
	}
}

func TestRenderSourceFormatsGo(t *testing.T) {
	src, err := renderSource("config", "Config", map[string]any{
		"http_port": int64(8080),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(src)
	if !strings.Contains(out, "package config") {
		t.Fatalf("missing package declaration:\n%s", out)
	}
	if !strings.Contains(out, "\tHTTPPort int64") {
		t.Fatalf("generated source is not gofmt-formatted:\n%s", out)
	}
}
