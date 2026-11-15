package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/fastabc/fastconf"
)

func TestLayeredConfig(t *testing.T) {
	t.Setenv("FC_EXAMPLE_SERVER_PORT", "9090")
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "port=9090 timeout=30s pool=24 listeners=[{public 8443} {admin 8081}]\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInvalidEnvironmentPreservesValidation(t *testing.T) {
	t.Setenv("FC_EXAMPLE_SERVER_PORT", "0")
	var out bytes.Buffer
	if err := run(&out); !errors.Is(err, fastconf.ErrInvalid) {
		t.Fatalf("expected invalid port, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("printed invalid configuration")
	}
}

func TestTimeoutValidation(t *testing.T) {
	t.Setenv("FC_EXAMPLE_SERVER_TIMEOUT", "0s")
	var out bytes.Buffer
	if err := run(&out); !errors.Is(err, fastconf.ErrInvalid) {
		t.Fatalf("expected invalid timeout, got %v", err)
	}
}
