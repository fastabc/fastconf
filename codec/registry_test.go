package codec

import (
	"testing"
)

// Content-type resolution returns the same codec, so HTTP sources
// inherit the rejection.
func TestYAMLContentTypeRejectsMultipleDocuments(t *testing.T) {
	c, _ := ByContentType("application/yaml")
	if _, err := c.Decode([]byte("a: 1\n---\nb: 2\n")); err == nil {
		t.Fatal("yaml Decode: want multi-document error")
	}
}

func TestByContentType(t *testing.T) {
	for _, ct := range []string{"application/yaml", "application/x-yaml", "text/yaml; charset=utf-8", "yml", ".yaml"} {
		c, ok := ByContentType(ct)
		if !ok {
			t.Fatalf("ByContentType(%q) not found", ct)
		}
		if _, err := c.Decode([]byte("a: 1\n")); err != nil {
			t.Fatalf("ByContentType(%q) decode: %v", ct, err)
		}
	}
	for _, ct := range []string{"application/json", "text/json", "json"} {
		c, ok := ByContentType(ct)
		if !ok {
			t.Fatalf("ByContentType(%q) not found", ct)
		}
		if _, err := c.Decode([]byte(`{"a":1}`)); err != nil {
			t.Fatalf("ByContentType(%q) decode: %v", ct, err)
		}
	}
	if _, ok := ByContentType("text/html"); ok {
		t.Fatal("text/html must not resolve to a codec")
	}
}
