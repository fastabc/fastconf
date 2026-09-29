package main

import (
	"bytes"
	"testing"
)

func TestINILayeredConfig(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "name=demo code=00123 port=9090 enabled=true timeout=5s\n"; got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}
