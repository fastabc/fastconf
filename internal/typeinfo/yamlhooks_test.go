package typeinfo

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Node stands in for another YAML library's node type, such as the one in
// gopkg.in/yaml.v3; only its name and package path matter.
type Node struct{}

type foreignHook struct{}

func (*foreignHook) UnmarshalYAML(*Node) error { return nil }

type bridgeHook struct {
	// Fields below a bridge hook are decoded by the hook, not the bridge.
	Inner foreignHook `yaml:"inner"`
}

func (*bridgeHook) UnmarshalYAML(*yaml.Node) error { return nil }

type legacyHook struct{}

func (*legacyHook) UnmarshalYAML(func(any) error) error { return nil }

func TestForeignYAMLNodeHook(t *testing.T) {
	type Embedded struct {
		Hook foreignHook `yaml:"hook"`
	}
	tests := []struct {
		name     string
		typ      reflect.Type
		wantPath string
	}{
		{"nested field", reflect.TypeFor[struct {
			DB struct {
				Addr *foreignHook `yaml:"addr"`
			} `yaml:"db"`
		}](), "db.addr"},
		{"collection element", reflect.TypeFor[struct {
			Hosts map[string][]foreignHook `yaml:"hosts"`
		}](), "hosts[][]"},
		{"inline embed", reflect.TypeFor[struct {
			Embedded `yaml:",inline"`
		}](), "hook"},
		{"untagged name", reflect.TypeFor[struct{ Addr foreignHook }](), "addr"},
		{"bridge hook stops descent", reflect.TypeFor[struct {
			B bridgeHook `yaml:"b"`
		}](), ""},
		{"skipped field", reflect.TypeFor[struct {
			Addr foreignHook `yaml:"-"`
		}](), ""},
		{"legacy hook", reflect.TypeFor[struct {
			L legacyHook `yaml:"l"`
		}](), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, pkg, ok := ForeignYAMLNodeHook(tc.typ)
			if ok != (tc.wantPath != "") || path != tc.wantPath {
				t.Fatalf("ForeignYAMLNodeHook = %q, %q, %v; want path %q", path, pkg, ok, tc.wantPath)
			}
			if ok && pkg != reflect.TypeFor[Node]().PkgPath() {
				t.Fatalf("pkg = %q", pkg)
			}
		})
	}
}
