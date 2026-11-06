package secret_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/fastabc/fastconf/internal/secret"
)

func TestPathHasSecrets_MapNamedInlineDoesNotLeak(t *testing.T) {
	type cfg struct {
		Inline map[string]string `json:"inline" yaml:"inline" fc:"secret"`
		Port   int               `json:"port"`
	}
	p := secret.Compile(reflect.TypeFor[cfg](), nil)
	if p.PathHasSecrets("port") {
		t.Fatal("sibling of a map named inline was masked")
	}
	if !p.PathHasSecrets("inline.x") {
		t.Fatal("secret map lost its mark")
	}
}

type pathLeaf struct {
	Name  string `json:"name"`
	Token string `json:"token" fc:"secret"`
}

type pathWrap struct {
	Items []pathLeaf          `json:"items"`
	ByKey map[string]pathLeaf `json:"byKey"`
}

// recordPaths records the paths passed by a compiled Plan to its redactor.
func recordPaths(tree map[string]any, t reflect.Type) []string {
	var seen []string
	secret.Compile(t, nil).Apply(tree, func(path string, _ any) any {
		seen = append(seen, path)
		return "***"
	})
	slices.Sort(seen)
	return seen
}

// Paths handed to a Redactor must never start with a separator.
//
// The struct branch already guarded against an empty parent path; the map
// branch did not, so a top-level map of structs produced ".db.token" and
// broke redactors that match on exact path text. Plan.Apply takes a
// map[string]any tree, so the map branch is the only one reachable with an
// empty parent — the slice branch shares the same joining helper for
// consistency but cannot be entered at depth zero.
func TestPlanApplyPathsHaveNoLeadingSeparator(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree map[string]any
		typ  reflect.Type
		want []string
	}{
		{
			name: "top-level map of structs",
			tree: map[string]any{"db": map[string]any{"name": "d", "token": "s"}},
			typ:  reflect.TypeOf(map[string]pathLeaf{}),
			want: []string{"db.token"},
		},
		{
			name: "top-level map with dotted key",
			tree: map[string]any{"eu.west": map[string]any{"name": "d", "token": "s"}},
			typ:  reflect.TypeOf(map[string]pathLeaf{}),
			want: []string{"eu.west.token"},
		},
		{
			name: "top-level map of slices of structs",
			tree: map[string]any{"eu": []any{map[string]any{"name": "d", "token": "s"}}},
			typ:  reflect.TypeOf(map[string][]pathLeaf{}),
			want: []string{"eu.0.token"},
		},
		{
			name: "nested slice keeps parent prefix",
			tree: map[string]any{"items": []any{map[string]any{"name": "d", "token": "s"}}},
			typ:  reflect.TypeOf(pathWrap{}),
			want: []string{"items.0.token"},
		},
		{
			name: "nested map keeps parent prefix",
			tree: map[string]any{"byKey": map[string]any{"a": map[string]any{"name": "d", "token": "s"}}},
			typ:  reflect.TypeOf(pathWrap{}),
			want: []string{"byKey.a.token"},
		},
		{
			name: "plain struct",
			tree: map[string]any{"name": "d", "token": "s"},
			typ:  reflect.TypeOf(pathLeaf{}),
			want: []string{"token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := recordPaths(tc.tree, tc.typ)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}
