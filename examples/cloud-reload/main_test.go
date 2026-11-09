package main

import (
	"bytes"
	"testing"
)

func TestNotificationLifecycle(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "recovered without notification: 2\npaused: acknowledged=false\nredelivered: value=3 acknowledged=true restarted=true\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}
