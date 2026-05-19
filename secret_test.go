package fastconf

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/pkg/source"
)

func reflectTypeOf(v any) reflect.Type { return reflect.TypeOf(v) }

func secretPathsFor(t reflect.Type) []string { return secret.Paths(t) }

type phase8Cfg struct {
	Name string `json:"name" yaml:"name"`
	DB   struct {
		DSN      string `json:"dsn" yaml:"dsn"`
		Password string `json:"password" yaml:"password" fastconf:"secret"`
	} `json:"db" yaml:"db"`
	Token string `json:"token" yaml:"token" fastconf:"secret"`
}

func TestSecret_PathScan(t *testing.T) {
	paths := secretPathsFor(reflectTypeOf(phase8Cfg{}))
	want := map[string]bool{"db.password": true, "token": true}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected secret path %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing secret paths: %v", want)
	}
}

func TestState_Redact(t *testing.T) {
	mgr, err := New[phase8Cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("a", "yaml", []byte("name: app\ndb:\n  dsn: real-dsn\n  password: hunter2\ntoken: tok-abc\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	out := mgr.Snapshot().Redact(nil)
	db, _ := out["db"].(map[string]any)
	if db["password"] != "***REDACTED***" {
		t.Fatalf("password not redacted: %v", db["password"])
	}
	if db["dsn"] != "real-dsn" {
		t.Fatalf("dsn should not be redacted: %v", db["dsn"])
	}
	if out["token"] != "***REDACTED***" {
		t.Fatalf("token not redacted: %v", out["token"])
	}
}

func TestRedactor_CustomFn(t *testing.T) {
	mgr, err := New[phase8Cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("a", "yaml", []byte("name: app\ndb:\n  dsn: x\n  password: hunter2\ntoken: t\n")), nil),
		WithSecretRedactor(func(path string, _ any) any { return "<" + path + ">" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	out := mgr.Snapshot().Redacted()
	db := out["db"].(map[string]any)
	if db["password"] != "<db.password>" {
		t.Fatalf("custom redactor not honored: %v", db["password"])
	}
}

type credCfg struct {
	Creds []struct {
		User     string `json:"user"`
		Password string `json:"password" fastconf:"secret"`
	} `json:"creds"`
	Tokens map[string]struct {
		Value string `json:"value" fastconf:"secret"`
	} `json:"tokens"`
}

func TestSecret_SliceAndMapElements(t *testing.T) {
	paths := secretPathsFor(reflectTypeOf(credCfg{}))
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

type TaggedSecretEmbed struct {
	Token string `json:"token" yaml:"token" fastconf:"secret"`
}

type taggedSecretCfg struct {
	TaggedSecretEmbed `json:"creds" yaml:"creds"`
}

func TestSecret_TaggedAnonymousEmbedUsesTaggedPath(t *testing.T) {
	paths := secretPathsFor(reflectTypeOf(taggedSecretCfg{}))
	want := map[string]bool{"creds.token": true}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected path %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}

	mgr, err := New[taggedSecretCfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("a", "yaml", []byte("creds:\n  token: hunter2\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	out := mgr.Snapshot().Redact(nil)
	creds, _ := out["creds"].(map[string]any)
	if creds["token"] != "***REDACTED***" {
		t.Fatalf("tagged anonymous token not redacted: %v", creds["token"])
	}
}

type cfg120 struct {
	DB struct {
		DSN string `json:"dsn"`
	} `json:"db"`
	Token string `json:"token"`
}

// fakeResolver recognises strings prefixed with "enc:" and returns the
// suffix as plaintext (or an error for the suffix "BOOM").
type fakeResolver struct {
	calls int
}

func (f *fakeResolver) Recognize(v string) (SecretRef, bool) {
	if strings.HasPrefix(v, "enc:") {
		return SecretRef{Scheme: "fake", Body: v[4:]}, true
	}
	return SecretRef{}, false
}

func (f *fakeResolver) Resolve(_ context.Context, ref SecretRef) (string, error) {
	f.calls++
	if ref.Body == "BOOM" {
		return "", errors.New("resolver-failed")
	}
	return ref.Body, nil
}

func TestSecretResolver_DecryptsBeforeDecode(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte(`
db:
  dsn: "enc:postgres://prod"
token: "enc:s3cret"
`),
		},
	}
	r := &fakeResolver{}
	mgr, err := New[cfg120](context.Background(),
		WithFS(fs),
		WithSecretResolver(r),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	got := mgr.Get()
	if got.DB.DSN != "postgres://prod" {
		t.Fatalf("dsn got %q", got.DB.DSN)
	}
	if got.Token != "s3cret" {
		t.Fatalf("token got %q", got.Token)
	}
	if r.calls != 2 {
		t.Fatalf("expected 2 resolver calls, got %d", r.calls)
	}
}

func TestSecretResolver_FailureSafe(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`token: "enc:BOOM"`)},
	}
	_, err := New[cfg120](context.Background(),
		WithFS(fs),
		WithSecretResolver(&fakeResolver{}),
	)
	if err == nil {
		t.Fatal("expected error on resolver failure")
	}
	if !errors.Is(err, ErrTransform) {
		t.Fatalf("expected ErrTransform, got %v", err)
	}
}

func TestSecretResolver_NoopWhenNotConfigured(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`token: "enc:literal-string"`)},
	}
	mgr, err := New[cfg120](context.Background(), WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if got := mgr.Get().Token; got != "enc:literal-string" {
		t.Fatalf("without resolver, value should pass through verbatim: got %q", got)
	}
}

func TestSecretResolverFunc_Boundaries(t *testing.T) {
	var zero SecretResolverFunc
	if ref, ok := zero.Recognize("enc:x"); ok {
		t.Fatalf("zero Recognize = (%+v, true), want false", ref)
	}
	if _, err := zero.Resolve(context.Background(), SecretRef{Scheme: "fake", Body: "x"}); err == nil {
		t.Fatal("zero Resolve should fail without ResolveFn")
	}
}
