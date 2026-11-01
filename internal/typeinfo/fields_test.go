package typeinfo_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/fastabc/fastconf/internal/typeinfo"
	"gopkg.in/yaml.v3"
)

func TestYAMLInline(t *testing.T) {
	for tag, want := range map[string]bool{
		",inline": true, "x,inline": true, ",omitempty,inline": true,
		"inline": false, "inline,omitempty": false, "": false, "-": false, ",flow": false,
	} {
		if got := typeinfo.YAMLInline(tag); got != want {
			t.Errorf("YAMLInline(%q)=%v want %v", tag, got, want)
		}
	}
}

func TestStructFields_FieldNamedInline(t *testing.T) {
	type sub struct{ Port int }
	type cfg struct {
		Inline sub `yaml:"inline"`
	}
	var names []string
	typeinfo.StructFields(reflect.TypeFor[cfg](), typeinfo.EmbedYAML, func(f reflect.StructField, _ []int) {
		names = append(names, f.Name)
	})
	if !slices.Equal(names, []string{"Inline"}) {
		t.Fatalf("fields = %v, want [Inline]", names)
	}
}

func TestStructFields_EmbedModes(t *testing.T) {
	type inner struct {
		A int
		b int //nolint:unused // Exercises ignored private fields.
	}
	type yamlInner struct{ C int }
	type T struct {
		inner
		Y yamlInner `yaml:",inline"`
		D int
	}
	// yaml.v3 omits the unexported embed and flattens exported inline fields.
	var decoded T
	if err := yaml.Unmarshal([]byte("a: 1\nc: 2\nd: 3"), &decoded); err != nil || decoded.A != 0 || decoded.Y.C != 2 || decoded.D != 3 {
		t.Fatalf("yaml behavior: %+v, %v", decoded, err)
	}
	for _, tc := range []struct {
		mode typeinfo.Embed
		want []string
	}{{typeinfo.EmbedJSON, []string{"A", "Y", "D"}}, {typeinfo.EmbedYAML, []string{"C", "D"}}, {0, []string{"Y", "D"}}} {
		var names []string
		typeinfo.StructFields(reflect.TypeFor[T](), tc.mode, func(f reflect.StructField, index []int) {
			names = append(names, f.Name)
			if reflect.TypeFor[T]().FieldByIndex(index).Name != f.Name {
				t.Fatal("invalid field index")
			}
		})
		if !slices.Equal(names, tc.want) {
			t.Fatalf("mode %d: %v, want %v", tc.mode, names, tc.want)
		}
	}
}

func TestReaches_RecursiveAndContainers(t *testing.T) {
	type node struct {
		Next *node
		Kids map[string][]node
		At   time.Time
	}
	if !typeinfo.Reaches(reflect.TypeFor[node](), func(t reflect.Type) bool { return t == reflect.TypeFor[time.Time]() }) {
		t.Fatal("time.Time unreachable")
	}
	seen := map[reflect.Type]bool{}
	if typeinfo.Reaches(reflect.TypeFor[node](), func(t reflect.Type) bool {
		if seen[t] {
			panic("visited twice")
		}
		seen[t] = true
		return false
	}) {
		t.Fatal("false predicate reached")
	}
	if typeinfo.Reaches(nil, func(reflect.Type) bool { return true }) {
		t.Fatal("nil reached")
	}
	type key struct{ N int }
	if !typeinfo.Reaches(reflect.TypeFor[map[key]string](), func(t reflect.Type) bool { return t == reflect.TypeFor[key]() }) {
		t.Fatal("map key unreachable")
	}
}

func TestStructFields_RecursiveEmbeds(t *testing.T) {
	type node struct {
		*node //nolint:unused // Exercises embedded cycles.
		A     int
	}
	var names []string
	typeinfo.StructFields(reflect.TypeFor[node](), typeinfo.EmbedJSON, func(f reflect.StructField, _ []int) { names = append(names, f.Name) })
	if !slices.Equal(names, []string{"A"}) {
		t.Fatalf("cycle: %v", names)
	}
}
