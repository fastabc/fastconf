package typeinfo_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

type Inner struct {
	A string `json:"a"`
	B int    `yaml:"b"`
}

type Outer struct {
	Name  string           `json:"name"`
	Inner Inner            `json:"inner"`
	PtrIn *Inner           `json:"ptr_in"`
	Slice []Inner          `json:"slice"`
	Map   map[string]Inner `json:"map"`
	Anon  struct {
		Hidden string `json:"hidden"`
	}
	private string //nolint:unused
}

type Embedded struct {
	Secret string `json:"secret"`
}

type UsesEmbed struct {
	Embedded
}

type collectVisitor struct{ paths []string }

func (v *collectVisitor) OnField(p string, _ []int, _ reflect.StructField) bool {
	v.paths = append(v.paths, p)
	return true
}
func (v *collectVisitor) OnStructEnter(_ string, _ reflect.Type) bool { return true }
func (v *collectVisitor) OnStructLeave(_ string, _ reflect.Type)      {}

func TestWalk_FieldDiscovery(t *testing.T) {
	var cv collectVisitor
	typeinfo.Walk(reflect.TypeOf(Outer{}), &cv)
	want := []string{
		"name",
		"inner", "inner.a", "inner.b",
		"ptr_in", "ptr_in.a", "ptr_in.b",
		"slice", "slice.[].a", "slice.[].b",
		"map", "map.{}.a", "map.{}.b",
		"anon", "anon.hidden",
	}
	if len(cv.paths) != len(want) {
		t.Fatalf("paths=%v\nwant=%v", cv.paths, want)
	}
	for i := range want {
		if cv.paths[i] != want[i] {
			t.Errorf("path[%d]=%q want %q", i, cv.paths[i], want[i])
		}
	}
}

func TestFieldName_PrefersJSON(t *testing.T) {
	type S struct {
		F string `json:"x" yaml:"y"`
	}
	f, _ := reflect.TypeOf(S{}).FieldByName("F")
	if got := typeinfo.FieldName(f); got != "x" {
		t.Fatalf("got %q want x", got)
	}
}

func TestFieldName_FallsBackToYAML(t *testing.T) {
	type S struct {
		F string `yaml:"y"`
	}
	f, _ := reflect.TypeOf(S{}).FieldByName("F")
	if got := typeinfo.FieldName(f); got != "y" {
		t.Fatalf("got %q want y", got)
	}
}

func TestFieldName_DefaultsLower(t *testing.T) {
	type S struct {
		Foo string
	}
	f, _ := reflect.TypeOf(S{}).FieldByName("Foo")
	if got := typeinfo.FieldName(f); got != "foo" {
		t.Fatalf("got %q want foo", got)
	}
}

func TestWalk_AnonymousEmbedFlattensChildren(t *testing.T) {
	var cv collectVisitor
	typeinfo.Walk(reflect.TypeOf(UsesEmbed{}), &cv)
	want := []string{"", "secret"}
	if len(cv.paths) != len(want) {
		t.Fatalf("paths=%v\nwant=%v", cv.paths, want)
	}
	for i := range want {
		if cv.paths[i] != want[i] {
			t.Errorf("path[%d]=%q want %q", i, cv.paths[i], want[i])
		}
	}
}

func TestCache_HitsAfterFirstCompute(t *testing.T) {
	c := typeinfo.NewCache[int]()
	calls := 0
	fn := func() int { calls++; return 42 }
	c.GetOrCompute(reflect.TypeOf(Outer{}), fn)
	c.GetOrCompute(reflect.TypeOf(Outer{}), fn)
	if calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}
}

func TestWalkFunc_AdaptsClosure(t *testing.T) {
	type X struct {
		A int
		B string
	}
	var got []string
	typeinfo.Walk(reflect.TypeOf(X{}), typeinfo.WalkFunc(func(_ string, _ []int, f reflect.StructField, _ *reflect.Type) bool {
		got = append(got, f.Name)
		return true
	}))
	if !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Errorf("got %v, want [A B]", got)
	}
}
