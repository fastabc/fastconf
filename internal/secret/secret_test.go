package secret_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/internal/secret"
)

func TestHasTag(t *testing.T) {
	if !secret.HasTag("secret") {
		t.Fatal("plain secret should be detected")
	}
	if !secret.HasTag("default=x,secret") {
		t.Fatal("secret after default should be detected")
	}
	if secret.HasTag("default=secret") {
		t.Fatal("default=secret should NOT match (value, not flag)")
	}
}

func TestWalkLeaves(t *testing.T) {
	m := map[string]any{
		"k1": "enc:a",
		"nested": map[string]any{
			"k2": "plain",
			"k3": "enc:b",
		},
		"list": []any{"enc:c", "plain"},
	}
	var rewrote []string
	secret.WalkLeaves(m, "", func(path, v string) (string, bool) {
		if strings.HasPrefix(v, "enc:") {
			rewrote = append(rewrote, path+"="+v)
			return v[4:], true
		}
		return v, false
	})
	if len(rewrote) != 3 {
		t.Fatalf("expected 3 rewrites, got %v", rewrote)
	}
	if m["k1"] != "a" {
		t.Fatalf("k1 not rewritten: %v", m["k1"])
	}
}

func TestResolverFunc_NilFn(t *testing.T) {
	var zero secret.ResolverFunc
	if _, ok := zero.Recognize("x"); ok {
		t.Fatal("zero Recognize should return false")
	}
	if _, err := zero.Resolve(context.Background(), secret.Ref{Scheme: "x"}); err == nil {
		t.Fatal("zero Resolve should error")
	}
}

func TestResolverFunc_PassThrough(t *testing.T) {
	f := secret.ResolverFunc{
		RecognizeFn: func(v string) (secret.Ref, bool) {
			if strings.HasPrefix(v, "enc:") {
				return secret.Ref{Scheme: "fake", Body: v[4:]}, true
			}
			return secret.Ref{}, false
		},
		ResolveFn: func(_ context.Context, r secret.Ref) (string, error) {
			if r.Body == "boom" {
				return "", errors.New("nope")
			}
			return r.Body, nil
		},
	}
	r, ok := f.Recognize("enc:hello")
	if !ok || r.Body != "hello" {
		t.Fatalf("recognize: %v %v", r, ok)
	}
	out, err := f.Resolve(context.Background(), secret.Ref{Body: "world"})
	if err != nil || out != "world" {
		t.Fatalf("resolve: %q %v", out, err)
	}
	if _, err := f.Resolve(context.Background(), secret.Ref{Body: "boom"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestWalkLeaves_NumericListPaths(t *testing.T) {
	tree := []any{"first", map[string]any{"nested": []any{"second"}}}
	var paths []string
	secret.WalkLeaves(tree, "", func(path, value string) (string, bool) { paths = append(paths, path); return value, false })
	if want := []string{"0", "1.nested.0"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// Historical values must stay masked even when their fields disappeared from
// the current snapshot, including dotted keys, lists and opaque marshalers.
func TestProvenanceSecretPaths(t *testing.T) {
	type credential struct {
		Token  string `json:"token" fc:"secret"`
		Public string `json:"public"`
	}
	type settings struct {
		Credentials []credential           `json:"credentials"`
		ByName      map[string]*credential `json:"by_name"`
		Plain       string                 `json:"plain"`
	}
	typ := reflect.TypeOf((*settings)(nil))
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"credentials", true}, {"credentials.0.token", true}, {"credentials.0.public", false},
		{"by_name.a.b.token", true}, {"by_name.a.public", true}, {"plain", false}, {"missing", false}, {"", true},
	} {
		if got := secret.PathHasSecrets(typ, tc.path); got != tc.want {
			t.Errorf("%q: got %v", tc.path, got)
		}
	}
	type recursive struct {
		Next   *recursive
		Public string
	}
	if secret.PathHasSecrets(reflect.TypeOf(recursive{}), "") {
		t.Fatal("recursive public type marked secret")
	}
	opaque := reflect.TypeOf(secretMarshaler{})
	if !secret.PathHasSecrets(opaque, "other") {
		t.Fatal("opaque secret container exposed")
	}
	got := secret.ApplyJSON(map[string]any{"other": "plaintext"}, opaque, nil)
	if got["$redacted"] != "***REDACTED***" {
		t.Fatalf("opaque output: %v", got)
	}
}

type secretMarshaler struct {
	Token string `fc:"secret"`
}

func (secretMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"other":"plaintext"}`), nil }

func TestProvenancePatternsMatchHistoricalContainers(t *testing.T) {
	value := map[string]any{"items": []any{map[string]any{"token": "old"}}}
	for _, tc := range []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"config", nil, false}, {"config", []string{"config.items.*.token"}, true},
		{"config.items.0.token", []string{"config"}, true}, {"config", []string{"other.**"}, false},
	} {
		if got := secret.PatternsMatchValue(value, tc.path, tc.patterns); got != tc.want {
			t.Errorf("%s/%v: got %v", tc.path, tc.patterns, got)
		}
	}
	if value["items"].([]any)[0].(map[string]any)["token"] != "old" {
		t.Fatal("matching mutated historical value")
	}
}
