package fastconf

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

type redactionCfg struct {
	Name string `json:"name" yaml:"name"`
	DB   struct {
		DSN      string `json:"dsn" yaml:"dsn"`
		Password string `json:"password" yaml:"password" fc:"secret"`
	} `json:"db" yaml:"db"`
	Token string `json:"token" yaml:"token" fc:"secret"`
}

func TestState_MapRedactsSecrets(t *testing.T) {
	mgr, err := New[redactionCfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("a", "yaml", []byte("name: app\ndb:\n  dsn: real-dsn\n  password: hunter2\ntoken: tok-abc\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	out := mgr.Snapshot().Map()
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
	mgr, err := New[redactionCfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("a", "yaml", []byte("name: app\ndb:\n  dsn: x\n  password: hunter2\ntoken: t\n"))),
		WithRedactor(func(path string, _ any) any { return "<" + path + ">" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	out := mgr.Snapshot().Map()
	db := out["db"].(map[string]any)
	if db["password"] != "<db.password>" {
		t.Fatalf("custom redactor not honored: %v", db["password"])
	}
}

type TaggedSecretEmbed struct {
	Token string `json:"token" yaml:"token" fc:"secret"`
}

type taggedSecretCfg struct {
	TaggedSecretEmbed `json:"creds" yaml:"creds"`
}

func TestSecret_TaggedAnonymousEmbedUsesTaggedPath(t *testing.T) {
	mgr, err := New[taggedSecretCfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("a", "yaml", []byte("creds:\n  token: hunter2\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	out := mgr.Snapshot().Map()
	creds, _ := out["creds"].(map[string]any)
	if creds["token"] != "***REDACTED***" {
		t.Fatalf("tagged anonymous token not redacted: %v", creds["token"])
	}
}

type secretResolverConfig struct {
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
	mgr, err := New[secretResolverConfig](context.Background(),
		WithFS(fs),
		WithSecretResolver(r),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
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
	_, err := New[secretResolverConfig](context.Background(),
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
	mgr, err := New[secretResolverConfig](context.Background(), WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Token; got != "enc:literal-string" {
		t.Fatalf("without resolver, value should pass through verbatim: got %q", got)
	}
}

const jsonSecretCanary = "json-secret-canary-7421"

type JSONSecretEmbedded struct {
	Embedded string `fc:"secret"`
}
type jsonSecretLeaf struct {
	Password string `fc:"secret"`
	Public   string
}
type jsonSecretOutput struct {
	JSONSecretEmbedded
	Password string `fc:"secret"`
	YAMLOnly string `yaml:"alias" fc:"secret"`
	Conflict string `json:"wire" yaml:"other" fc:"secret"`
	Dotted   string `json:"literal.dot" fc:"secret"`
	EmptyTag string `json:",omitempty" fc:"secret"`
	Ignored  string `json:"-" fc:"secret"`
	Nested   *jsonSecretLeaf
	List     [][]*jsonSecretLeaf
	Map      map[string][]jsonSecretLeaf
	Public   string
}

func TestSecretJSONOutputNames(t *testing.T) {
	leaf := jsonSecretLeaf{Password: jsonSecretCanary, Public: "visible"}
	value := jsonSecretOutput{
		JSONSecretEmbedded: JSONSecretEmbedded{jsonSecretCanary},
		Password:           jsonSecretCanary, YAMLOnly: jsonSecretCanary, Conflict: jsonSecretCanary,
		Dotted: jsonSecretCanary, EmptyTag: jsonSecretCanary, Ignored: jsonSecretCanary,
		Nested: &leaf, List: [][]*jsonSecretLeaf{{&leaf}}, Map: map[string][]jsonSecretLeaf{"a": {leaf}}, Public: "visible",
	}
	assertSecretOutputs(t, value)
}

type jsonOpaqueSecret struct {
	Password string `fc:"secret"`
}

func (v jsonOpaqueSecret) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"unexpected": v.Password})
}

func TestSecretOpaqueMarshalerFailsClosed(t *testing.T) {
	assertSecretOutputs(t, struct {
		Opaque jsonOpaqueSecret
		Public string
	}{jsonOpaqueSecret{jsonSecretCanary}, "visible"})
}

type jsonSecretShadow struct {
	Public string `fc:"secret"`
}
type jsonSecretDominant struct {
	jsonSecretShadow
	Public   string
	Password string `fc:"secret"`
}

func TestSecretShadowedFieldDoesNotMaskPublicWinner(t *testing.T) {
	assertSecretOutputs(t, jsonSecretDominant{jsonSecretShadow{jsonSecretCanary}, "visible", jsonSecretCanary})
}

func TestSecretPreviewAndReporterAreSafe(t *testing.T) {
	type cfg struct {
		Password string `fc:"secret"`
	}
	fs := fstest.MapFS{"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{"Password":"old-secret"}`)}}
	events := make(chan Committed, 1)
	m, err := New[cfg](context.Background(), WithFS(fs), WithObserver(observerFunc(func(_ context.Context, e Event) {
		if c, ok := e.(Committed); ok && c.Prev != 0 {
			events <- c
		}
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	fs["conf.d/base/00.json"] = &fstest.MapFile{Data: []byte(`{"Password":"` + jsonSecretCanary + `"}`)}
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != before {
		t.Fatal("preview published")
	}
	check := func(entries []DiffEntry) {
		t.Helper()
		if len(entries) != 1 || entries[0].Path != "Password" {
			t.Fatal("secret-only change disappeared")
		}
		if entries[0].Before != "***REDACTED***" || entries[0].After != "***REDACTED***" {
			t.Fatal("diagnostic diff exposed a secret")
		}
	}
	check(plan.Diff)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		check(e.Diff())
	case <-time.After(2 * time.Second):
		t.Fatal("reporter did not receive committed change")
	}
	if m.Get().Password != jsonSecretCanary {
		t.Fatal("business value was redacted")
	}
}

func assertSecretOutputs[T any](t *testing.T, value T) {
	t.Helper()
	m, err := New[T](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("input", "json", []byte(`{}`))),
		WithValidate(func(out *T) error { *out = value; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	raw, err := json.Marshal(m.Get())
	if err != nil || !strings.Contains(string(raw), jsonSecretCanary) {
		t.Fatal("business value lost its secret")
	}
	for _, tree := range []map[string]any{m.Snapshot().Map(), m.Snapshot().Map()} {
		b, _ := json.Marshal(tree)
		if strings.Contains(string(b), jsonSecretCanary) {
			t.Fatal("safe tree exposed secret")
		}
		if tree["Public"] != "visible" {
			t.Fatal("ordinary field changed")
		}
	}
	for _, format := range []Format{JSON, YAML, TOML} {
		b, err := m.Snapshot().Dump(format)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), jsonSecretCanary) {
			t.Fatalf("%s exposed secret", format)
		}
	}
}

// TestSecretPathsRedactMapTypedConfig verifies that a map-typed Manager has no
// struct tags, so WithSecretPaths is the only way its secrets get masked.
func TestSecretPathsRedactMapTypedConfig(t *testing.T) {
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("db:\n  host: h\n  password: hunter2\n")}}
	mgr, err := New[map[string]any](context.Background(), WithFS(fs), WithSecretPaths("*.password"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	st := mgr.Snapshot()
	db, _ := st.Map()["db"].(map[string]any)
	if db["password"] != "***REDACTED***" || db["host"] != "h" {
		t.Fatalf("Redacted() = %v; want password masked, host kept", db)
	}
	out, err := st.Dump(JSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "hunter2") {
		t.Fatalf("redacted Dump leaked secret: %s", out)
	}
	if got := (*mgr.Get())["db"].(map[string]any)["password"]; got != "hunter2" {
		t.Fatalf("Get() must stay unredacted, got %v", got)
	}
}

func TestSecretResolver_ListElementProvenancePath(t *testing.T) {
	m, err := New[map[string]any](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("list: [enc:private]\n")}}),
		WithSecretResolver(&fakeResolver{}), WithProvenance(ProvenanceFull), WithSecretPaths("list.0"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	origins := m.Snapshot().Explain("list.0")
	if len(origins) != 1 || origins[0].Source.Path != "secret://fake" {
		t.Fatalf("numeric list path origins: %+v", origins)
	}
	if got := m.Snapshot().Map()["list"].([]any)[0]; got != "***REDACTED***" {
		t.Fatalf("list secret leaked: %v", got)
	}
	if got := m.Snapshot().Explain("list.[0]"); len(got) != 0 {
		t.Fatalf("bracket path still present: %+v", got)
	}
}
