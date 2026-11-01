package secret_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/internal/secret"
)

func TestPlanApplyPatternsMasksMatchingPaths(t *testing.T) {
	tree := map[string]any{
		"db":    map[string]any{"password": "p1", "host": "h"},
		"cache": map[string]any{"auth": map[string]any{"token": "t1"}},
		"list":  []any{map[string]any{"token": "t2", "id": "x"}},
		"token": "t0",
		"dsn":   "d",
	}
	var paths []string
	got := secret.Compile(reflect.TypeFor[map[string]any](), []string{"*.password", "**.token", "dsn"}).Apply(tree, func(path string, _ any) any {
		paths = append(paths, path)
		return "***"
	})
	want := map[string]any{
		"db":    map[string]any{"password": "***", "host": "h"},
		"cache": map[string]any{"auth": map[string]any{"token": "***"}},
		"list":  []any{map[string]any{"token": "***", "id": "x"}},
		"token": "***",
		"dsn":   "***",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan.Apply:\n got %v\nwant %v", got, want)
	}
	if len(paths) != 5 {
		t.Fatalf("redactor calls = %v; want 5 dotted paths", paths)
	}
}

func TestPlanApplyPatternsSingleStarMatchesOneSegment(t *testing.T) {
	tree := map[string]any{"a": map[string]any{"b": map[string]any{"password": "deep"}}}
	secret.Compile(reflect.TypeFor[map[string]any](), []string{"*.password"}).Apply(tree, secret.DefaultRedactor)
	if got := tree["a"].(map[string]any)["b"].(map[string]any)["password"]; got != "deep" {
		t.Fatalf("*.password must not match a.b.password, got %v", got)
	}
}

func TestPlanApplyPatternsMasksWholeSubtree(t *testing.T) {
	tree := map[string]any{"creds": map[string]any{"user": "u", "pass": "p"}}
	secret.Compile(reflect.TypeFor[map[string]any](), []string{"creds"}).Apply(tree, nil)
	if got := tree["creds"]; got != "***REDACTED***" {
		t.Fatalf("matched container must be masked as a whole, got %v", got)
	}
}
