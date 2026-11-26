package typeinfo

import (
	"reflect"
	"strconv"
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

func BenchmarkInjectTagDefaultsPresent(b *testing.B) {
	type config struct {
		Port int `json:"port" yaml:"port" default:"80"`
	}
	for _, tc := range []struct {
		name string
		yaml bool
	}{{"JSON", false}, {"YAML", true}} {
		b.Run(tc.name, func(b *testing.B) {
			defs, err := TagDefaultsFor(reflect.TypeFor[config](), tc.yaml)
			if err != nil {
				b.Fatal(err)
			}
			tree := map[string]any{"port": 0}
			for i := range 64 {
				tree["setting"+strconv.Itoa(i)] = i
			}
			b.ReportAllocs()
			for b.Loop() {
				InjectTagDefaults(tree, defs, false, nil)
			}
		})
	}
}
