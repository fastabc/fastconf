package codec

import (
	"reflect"
	"strings"
	"testing"
)

// A YAML stream that carries more than one content document must be
// rejected rather than silently truncated to the first document, matching
// the jsonDecoder contract. An empty trailing document (a lone "---", a
// trailing separator followed by whitespace or a comment) carries nothing
// and stays accepted: templating tools emit those routinely and rejecting
// them would break working configurations without preventing any loss.
func TestYAMLDecoderRejectsMultipleDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"two documents", "a: 1\n---\nb: 2\n"},
		{"three documents", "a: 1\n---\nb: 2\n---\nc: 3\n"},
		{"content after empty document", "a: 1\n---\n\n---\nb: 2\n"},
		{"empty first document then content", "---\n---\na: 1\n"},
		{"sequence as second document", "a: 1\n---\n- x\n"},
		{"scalar as second document", "a: 1\n---\n42\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamlDecoder{}.Decode([]byte(tc.in))
			if err == nil {
				t.Fatalf("Decode(%q) = %v, want multi-document error", tc.in, got)
			}
			if !strings.Contains(err.Error(), "multiple documents") {
				t.Fatalf("Decode(%q) error = %v, want it to mention multiple documents", tc.in, err)
			}
		})
	}
}

func TestYAMLDecoderAcceptsSingleDocument(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want map[string]any
	}{
		{"plain mapping", "a: 1\n", map[string]any{"a": 1}},
		{"leading separator", "---\na: 1\n", map[string]any{"a": 1}},
		{"document end marker", "a: 1\n...\n", map[string]any{"a": 1}},
		{"trailing separator", "a: 1\n---\n", map[string]any{"a": 1}},
		{"trailing separator and blank line", "a: 1\n---\n\n", map[string]any{"a": 1}},
		{"trailing separator and comment", "a: 1\n---\n# nothing\n", map[string]any{"a": 1}},
		{"empty input", "", map[string]any{}},
		{"whitespace only", "   \n\n", map[string]any{}},
		{"comment only", "# nothing here\n", map[string]any{}},
		// Boundary-looking bytes that are not document boundaries: these take
		// the strict decoder path and must still resolve to one document.
		{"separator inside block scalar", "a: 1\nnote: |\n  ---\n  still scalar\n",
			map[string]any{"a": 1, "note": "---\nstill scalar\n"}},
		{"end marker inside block scalar", "a: 1\nnote: |\n  ...\n  still scalar\n",
			map[string]any{"a": 1, "note": "...\nstill scalar\n"}},
		{"explicit null document", "null\n", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamlDecoder{}.Decode([]byte(tc.in))
			if err != nil {
				t.Fatalf("Decode(%q) unexpected error: %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Decode(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Fatalf("Decode(%q)[%q] = %v, want %v", tc.in, k, got[k], want)
				}
			}
		})
	}
}

// Malformed content after the first document is reported, not swallowed.
func TestYAMLDecoderReportsTrailingParseError(t *testing.T) {
	_, err := yamlDecoder{}.Decode([]byte("a: 1\n---\n\tbad\n"))
	if err == nil {
		t.Fatal("Decode: want error for malformed trailing document")
	}
}

// DecodeAny is the patch-layer path (.patch.yaml / .patch.json). Its JSON
// branch already rejected multi-document input; the YAML branch must match,
// otherwise a multi-document patch file silently applies only its first
// document.
func TestDecodeAnyYAMLRejectsMultipleDocuments(t *testing.T) {
	for _, in := range []string{
		"- op: add\n---\n- op: remove\n",
		"a: 1\n---\nb: 2\n",
		"- op: add\n---\n\n---\n- op: remove\n",
	} {
		got, err := DecodeAny("yaml", []byte(in))
		if err == nil {
			t.Fatalf("DecodeAny(yaml, %q) = %v, want multi-document error", in, got)
		}
		if !strings.Contains(err.Error(), "multiple documents") {
			t.Fatalf("DecodeAny(yaml, %q) error = %v, want it to mention multiple documents", in, err)
		}
	}
}

func TestDecodeAny_YAML(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        any
	}{
		{"patch", "- op: add\n  path: /a\n", []any{map[string]any{"op": "add", "path": "/a"}}},
		{"object", "a: 1\n", map[string]any{"a": 1}},
		{"trailing separator", "- op: add\n  path: /a\n---\n", []any{map[string]any{"op": "add", "path": "/a"}}},
		{"trailing comment document", "a: 1\n---\n# nothing\n", map[string]any{"a": 1}},
		{"empty", "", nil},
		{"comment only", "# nothing\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeAny("yaml", []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DecodeAny=%#v; want %#v", got, tc.want)
			}
		})
	}
}
