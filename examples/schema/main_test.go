package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExample(t *testing.T) {
	var b bytes.Buffer
	if err := example(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config.schema.json", "config.example.yaml", ".env.example", "port: 8080", "APP_DATABASE__HOSTS=a,b"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
}
