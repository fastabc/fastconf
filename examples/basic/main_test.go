package main

import (
	"bytes"
	"testing"
)

func TestLayeredConfig(t *testing.T) {
	t.Setenv("FC_EXAMPLE_SERVER_PORT", "9090")
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "port=9090 timeout=30s pool=20\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
