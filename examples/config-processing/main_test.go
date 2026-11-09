package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
)

func TestProcessing(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want := "enabled=false retries=0 timeout=5s default-source=true\n" +
		"level=3 feature=true hosts=[api-a api-b] ports=[8080 9090] regions=[eu us]\n" +
		"callback=https://example.test/callback owner=demo literal=${NOT_EXPANDED}\n"
	if out.String() != want {
		t.Fatalf("got:\n%swant:\n%s", &out, want)
	}
}

func TestRequiredEnvironmentAndLiteralLookup(t *testing.T) {
	if _, err := load(func(string) (string, bool) { return "", false }); !errors.Is(err, fastconf.ErrTransform) {
		t.Fatalf("missing OWNER: %v", err)
	}
	st, err := load(func(name string) (string, bool) {
		if name == "HOST" {
			return "${UNTRUSTED}", true
		}
		if name == "OWNER" {
			return "demo", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Value().Callback, "${UNTRUSTED}") {
		t.Fatal("lookup content was expanded twice")
	}
}
