package codec

import (
	"reflect"
	"testing"
)

func TestTOMLDecoder_FlatAndNested(t *testing.T) {
	src := []byte(`
name = "edge"
port = 8080
debug = true

[server]
addr = "0.0.0.0"
read_timeout = 30

[[server.routes]]
path = "/healthz"
method = "GET"

[[server.routes]]
path = "/version"
method = "GET"
`)
	got, err := tomlDecoder{}.Decode(src)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["name"] != "edge" {
		t.Errorf("name = %v, want edge", got["name"])
	}
	if got["debug"] != true {
		t.Errorf("debug = %v, want true", got["debug"])
	}
	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("server: want map[string]any, got %T", got["server"])
	}
	if server["addr"] != "0.0.0.0" {
		t.Errorf("server.addr = %v, want 0.0.0.0", server["addr"])
	}
	// Arrays-of-tables must normalise into []any of map[string]any so the
	// deep merger can walk them like the YAML / JSON shapes.
	routes, ok := server["routes"].([]any)
	if !ok {
		t.Fatalf("server.routes: want []any after normalisation, got %T", server["routes"])
	}
	if len(routes) != 2 {
		t.Fatalf("routes length = %d, want 2", len(routes))
	}
	first, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("routes[0]: want map[string]any, got %T", routes[0])
	}
	if first["path"] != "/healthz" {
		t.Errorf("routes[0].path = %v, want /healthz", first["path"])
	}
}

func TestTOMLDecoder_EmptyAndInvalid(t *testing.T) {
	got, err := tomlDecoder{}.Decode(nil)
	if err != nil {
		t.Fatalf("empty: err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("empty: got %v, want empty map", got)
	}

	_, badErr := tomlDecoder{}.Decode([]byte("not = toml = at all"))
	if badErr == nil {
		t.Fatal("expected error for malformed TOML")
	}
}

func TestTOMLDecoder_RegistryWired(t *testing.T) {
	c, err := For("toml")
	if err != nil {
		t.Fatalf("For(toml): %v", err)
	}
	out, err := c.Decode([]byte(`key = "value"`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out["key"] != "value" {
		t.Errorf("key = %v, want value", out["key"])
	}
	if ext := LookupExt(".toml"); ext != "toml" {
		t.Errorf("LookupExt(.toml) = %q, want toml", ext)
	}
}
