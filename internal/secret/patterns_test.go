package secret_test

import (
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/internal/secret"
)

func TestApplyPatternsMasksMatchingPaths(t *testing.T) {
	tree := map[string]any{
		"db":    map[string]any{"password": "p1", "host": "h"},
		"cache": map[string]any{"auth": map[string]any{"token": "t1"}},
		"list":  []any{map[string]any{"token": "t2", "id": "x"}},
		"token": "t0",
		"dsn":   "d",
	}
	var paths []string
	got := secret.ApplyPatterns(tree, []string{"*.password", "**.token", "dsn"}, func(path string, _ any) any {
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
		t.Fatalf("ApplyPatterns:\n got %v\nwant %v", got, want)
	}
	if len(paths) != 5 {
		t.Fatalf("redactor calls = %v; want 5 dotted paths", paths)
	}
}

func TestApplyPatternsSingleStarMatchesOneSegment(t *testing.T) {
	tree := map[string]any{"a": map[string]any{"b": map[string]any{"password": "deep"}}}
	secret.ApplyPatterns(tree, []string{"*.password"}, secret.DefaultRedactor)
	if got := tree["a"].(map[string]any)["b"].(map[string]any)["password"]; got != "deep" {
		t.Fatalf("*.password must not match a.b.password, got %v", got)
	}
}

func TestApplyPatternsMasksWholeSubtree(t *testing.T) {
	tree := map[string]any{"creds": map[string]any{"user": "u", "pass": "p"}}
	secret.ApplyPatterns(tree, []string{"creds"}, nil)
	if got := tree["creds"]; got != "***REDACTED***" {
		t.Fatalf("matched container must be masked as a whole, got %v", got)
	}
}
