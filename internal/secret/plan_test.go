package secret

import (
	"reflect"
	"testing"
)

func TestPlanWithExactPaths(t *testing.T) {
	type C struct {
		Annotations map[string]string `json:"annotations"`
	}
	base := Compile(reflect.TypeFor[C](), nil)
	path := []string{"annotations", "example.com/token"}
	plan := base.WithExactPaths([][]string{path})
	path[1] = "changed"
	tree := map[string]any{"annotations": map[string]any{"example.com/token": "plain", "other": "visible"}}
	plan.Apply(tree, nil)
	a := tree["annotations"].(map[string]any)
	if a["example.com/token"] != "***REDACTED***" || a["other"] != "visible" {
		t.Fatal(a)
	}
	if !plan.PatternsMatch("annotations.example.com/token", "plain") || base.PatternsMatch("annotations.example.com/token", "plain") {
		t.Fatal("path plan mutated or failed to match")
	}
}
