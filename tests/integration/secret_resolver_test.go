package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fastabc/fastconf"
)

func testPrefix(scheme, prefix, plain string) fastconf.SecretResolver {
	return fastconf.PrefixSecretResolver(scheme, prefix, func(context.Context, string) (string, error) { return plain, nil })
}
func TestSecretResolvers_FirstRecognizerWins(t *testing.T) {
	type C struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	p := &snapshotProvider{name: "test", data: map[string]any{"a": "ref+v://x", "b": "age:y"}}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(p), fastconf.WithProvenance(fastconf.ProvenanceFull), fastconf.WithSecretResolvers(nil, testPrefix("v", "ref+v://", "vault"), testPrefix("later", "ref+v://", "wrong"), testPrefix("age", "age:", "age")))
	if err != nil {
		t.Fatal(err)
	}
	if *st.Value() != (C{"vault", "age"}) {
		t.Fatal(st.Value())
	}
	o := st.Explain("a")
	if len(o) == 0 || o[len(o)-1].Source.Path != "secret://v" {
		t.Fatal(o)
	}
}
func TestWithSecretResolver_ReplacesList(t *testing.T) {
	type C struct {
		A string `json:"a"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(&snapshotProvider{name: "p", data: map[string]any{"a": "a:x"}}), fastconf.WithSecretResolvers(testPrefix("a", "a:", "plain")), fastconf.WithSecretResolver(testPrefix("b", "b:", "plain")))
	if err != nil || st.Value().A != "a:x" {
		t.Fatalf("%v %v", st, err)
	}
}
func TestResolvedSecrets_MaskedInDiagnostics(t *testing.T) {
	type C struct {
		DSN string `json:"dsn"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(&snapshotProvider{name: "p", data: map[string]any{"dsn": "ref+t://dsn"}}), fastconf.WithSecretResolvers(testPrefix("t", "ref+t://", "plain")), fastconf.WithProvenance(fastconf.ProvenanceFull))
	if err != nil {
		t.Fatal(err)
	}
	if st.Value().DSN != "plain" || st.Map()["dsn"] != "***REDACTED***" || st.Unredacted().Map()["dsn"] != "plain" {
		t.Fatal(st.Map())
	}
	buf, err := st.Dump(fastconf.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(buf), "plain") {
		t.Fatal("dump leaked")
	}
	for _, o := range st.Explain("dsn") {
		if o.Value == "plain" {
			t.Fatal("Explain leaked")
		}
	}
}
func TestResolvedSecrets_MaskedWithDottedKey(t *testing.T) {
	type C struct {
		Annotations map[string]string `json:"annotations"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(&snapshotProvider{name: "p", data: map[string]any{"annotations": map[string]any{"example.com/token": "ref+t://x", "other": "visible"}}}), fastconf.WithSecretResolvers(testPrefix("t", "ref+t://", "plain")))
	if err != nil {
		t.Fatal(err)
	}
	a := st.Map()["annotations"].(map[string]any)
	if a["example.com/token"] != "***REDACTED***" || a["other"] != "visible" {
		t.Fatal(a)
	}
}
func TestResolvedSecrets_DiffMasksRotationAndRollback(t *testing.T) {
	type C struct {
		DSN string `json:"dsn"`
	}
	plain := "v1"
	r := fastconf.PrefixSecretResolver("t", "ref+t://", func(context.Context, string) (string, error) { return plain, nil })
	m, err := fastconf.New[C](context.Background(), fastconf.WithProvider(&snapshotProvider{name: "p", data: map[string]any{"dsn": "ref+t://dsn"}}), fastconf.WithSecretResolvers(r), fastconf.WithHistory(2))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	plain = "v2"
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	diff := before.Diff(m.Snapshot())
	if len(diff) != 1 || diff[0].Before != "***REDACTED***" || diff[0].After != "***REDACTED***" {
		t.Fatalf("%+v", diff)
	}
	if err := m.History().Rollback(before); err != nil {
		t.Fatal(err)
	}
	if m.Get().DSN != "v1" || m.Snapshot().Map()["dsn"] != "***REDACTED***" {
		t.Fatal(m.Snapshot().Map())
	}
}
