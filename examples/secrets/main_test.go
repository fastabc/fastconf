package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSecretRotation(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "runtime-ready=true diagnostics=map[api_key:***REDACTED*** password:***REDACTED***]\n" +
		"preview changes=1 live-preserved=true\n" +
		"~ api_key : ***REDACTED*** -> ***REDACTED***\n" +
		"resolution failed: preserved=true\n"
	if out.String() != want {
		t.Fatalf("got:\n%swant:\n%s", &out, want)
	}
	for _, secret := range []string{"demo-key-one", "demo-key-two", "demo-vault-password"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("printed plaintext")
		}
	}
}
