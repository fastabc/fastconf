//go:build ignore

package main

import (
	"bytes"
	"testing"
)

func TestJSONSettings(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "default limits type=string\njson settings: host=localhost rps=10 enabled=true\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}
