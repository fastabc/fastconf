package parser_test

import (
	"testing"

	"github.com/fastabc/fastconf/pkg/parser"
)

func TestLookup_YAMLByExtension(t *testing.T) {
	for _, ct := range []string{"yaml", ".yaml", ".yml", "yml", "application/yaml", "APPLICATION/YAML"} {
		p, ok := parser.Lookup(ct)
		if !ok {
			t.Errorf("Lookup(%q) miss", ct)
			continue
		}
		got, err := p.Decode([]byte("a: 1\n"))
		if err != nil {
			t.Fatalf("decode via %q: %v", ct, err)
		}
		if got["a"] != 1 {
			t.Errorf("decoded %v for %q", got, ct)
		}
	}
}

func TestLookup_JSON(t *testing.T) {
	p, ok := parser.Lookup(".json")
	if !ok {
		t.Fatal("json not registered")
	}
	got, err := p.Decode([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"]; !ok {
		t.Errorf("missing a in %v", got)
	}
}

func TestLookup_TOML(t *testing.T) {
	p, ok := parser.Lookup("toml")
	if !ok {
		t.Fatal("toml not registered")
	}
	got, err := p.Decode([]byte("a = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"]; !ok {
		t.Errorf("missing a in %v", got)
	}
}

func TestLookup_EmptyAndUnknown(t *testing.T) {
	if _, ok := parser.Lookup(""); ok {
		t.Error("empty content-type should not resolve")
	}
	if _, ok := parser.Lookup("application/no-such-format"); ok {
		t.Error("unknown content-type should not resolve")
	}
}

func TestBuiltins_DeclareContentTypes(t *testing.T) {
	for _, p := range []func() interface{ ContentTypes() []string }{
		func() interface{ ContentTypes() []string } { return parser.YAML() },
		func() interface{ ContentTypes() []string } { return parser.JSON() },
		func() interface{ ContentTypes() []string } { return parser.TOML() },
	} {
		if got := p().ContentTypes(); len(got) == 0 {
			t.Errorf("built-in declared no content-types: %T", p())
		}
	}
}
