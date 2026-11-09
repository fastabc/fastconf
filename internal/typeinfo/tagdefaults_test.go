package typeinfo

import (
	"reflect"
	"testing"
)

func TestTagDefaultsFor_Paths(t *testing.T) {
	type DB struct {
		Pool int `json:"pool" default:"10"`
	}
	type C struct {
		DB *DB `json:"db"`
	}
	defs, err := TagDefaultsFor(reflect.TypeFor[C](), false)
	if err != nil || len(defs) != 1 {
		t.Fatalf("%+v %v", defs, err)
	}
	if !reflect.DeepEqual(defs[0].Keys, []string{"db", "pool"}) || !reflect.DeepEqual(defs[0].Optional, []bool{true, false}) {
		t.Fatalf("%+v", defs)
	}
}
func TestInjectTagDefaults_IgnoresOtherBridgeAlias(t *testing.T) {
	type C struct {
		MaxConns int `json:"maxConns" yaml:"max_conns" default:"5"`
	}
	defs, err := TagDefaultsFor(reflect.TypeFor[C](), false)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"max_conns": 7}
	InjectTagDefaults(m, defs, false, nil)
	if len(m) != 2 || m["max_conns"] != 7 || m["maxConns"] != "5" {
		t.Fatalf("%v", m)
	}
}
