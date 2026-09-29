package main

import (
	"bytes"
	"testing"
)

func TestPreviewReloadRollback(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "invalid preview: violations=1 live=8080\n" +
		"rejected: preserved=true\n" +
		"valid preview: proposed=9090 live=8080 changes=1\n" +
		"committed: port=9090 generation=2\n" +
		"rollback: port=8080 generation=3\n"
	if out.String() != want {
		t.Fatalf("got:\n%swant:\n%s", &out, want)
	}
}
