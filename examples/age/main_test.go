//go:build ignore

package main

import (
	"bytes"
	"testing"
)

func TestAgeRoundTrip(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "decrypted=true diagnostic=***REDACTED***\n" {
		t.Fatal(out.String())
	}
}
