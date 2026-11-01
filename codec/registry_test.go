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
	for _, tc := range []struct {
		input string
		types []string
	}{
		{"a: 1\n", []string{"application/yaml", "application/x-yaml", "text/yaml; charset=utf-8", "yml", ".yaml"}},
		{`{"a":1}`, []string{"application/json", "text/json", "json"}},
	} {
		for _, ct := range tc.types {
			t.Run(ct, func(t *testing.T) {
				c, ok := ByContentType(ct)
				if !ok {
					t.Fatalf("ByContentType(%q) not found", ct)
				}
				if _, err := c.Decode([]byte(tc.input)); err != nil {
					t.Fatalf("decode: %v", err)
				}
			})
		}
	}
	if _, ok := ByContentType("text/html"); ok {
		t.Fatal("text/html must not resolve to a codec")
	}
}
