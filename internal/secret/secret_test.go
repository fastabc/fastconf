package secret_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/internal/secret"
)

type embeddedCfg struct {
	Name string `json:"name"`
	DB   struct {
		DSN      string `json:"dsn"`
		Password string `json:"password" fastconf:"secret"`
	} `json:"db"`
	Token string `json:"token" fastconf:"secret"`
}

func TestPaths_FindsSecretFields(t *testing.T) {
	paths := secret.Paths(reflect.TypeFor[embeddedCfg]())
	want := map[string]bool{"db.password": true, "token": true}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected path %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}
}

type sliceCfg struct {
	Creds []struct {
		User     string `json:"user"`
		Password string `json:"password" fastconf:"secret"`
	} `json:"creds"`
	Tokens map[string]struct {
		Value string `json:"value" fastconf:"secret"`
	} `json:"tokens"`
}

func TestPaths_SliceAndMap(t *testing.T) {
	paths := secret.Paths(reflect.TypeFor[sliceCfg]())
	want := map[string]bool{"creds.[].password": true, "tokens.{}.value": true}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected path %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}
}

func TestApply_RewritesPath(t *testing.T) {
	m := map[string]any{
		"db":    map[string]any{"dsn": "real", "password": "hunter2"},
		"token": "abc",
	}
	secret.Apply(m, []string{"db.password", "token"}, nil)
	db := m["db"].(map[string]any)
	if db["password"] != "***REDACTED***" {
		t.Fatalf("password not redacted: %v", db["password"])
	}
	if db["dsn"] != "real" {
		t.Fatalf("dsn changed: %v", db["dsn"])
	}
	if m["token"] != "***REDACTED***" {
		t.Fatalf("token not redacted: %v", m["token"])
	}
}

func TestApply_SliceElement(t *testing.T) {
	m := map[string]any{
		"creds": []any{
			map[string]any{"user": "u", "password": "p1"},
			map[string]any{"user": "v", "password": "p2"},
		},
	}
	secret.Apply(m, []string{"creds.[].password"}, nil)
	arr := m["creds"].([]any)
	for _, item := range arr {
		got := item.(map[string]any)["password"]
		if got != "***REDACTED***" {
			t.Fatalf("password not redacted: %v", got)
		}
	}
}

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
