package integration_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

func TestPatchProvenanceAttributesOnlyTouchedPaths(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/20-database.yaml": &fstest.MapFile{Data: []byte(`
database:
  dsn: postgres://base
  pool: 10
`)},
		"conf.d/overlays/prod/30-database.patch.yaml": &fstest.MapFile{Data: []byte(`
- op: replace
  path: /database/dsn
  value: postgres://prod-patched
`)},
	}
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithProvenance(fastconf.ProvenanceFull),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	snap := mgr.Snapshot()

	// The patch only touched database.dsn; database.pool must not list it.
	for _, o := range snap.Explain("database.pool") {
		if o.Source.Kind == fastconf.LayerPatch {
			t.Errorf("untouched database.pool wrongly attributed to patch: %+v", o.Source)
		}
	}

	// database.dsn's winner (chain tail) must be the patch.
	chain := snap.Explain("database.dsn")
	if len(chain) == 0 {
		t.Fatal("no origin recorded for database.dsn")
	}
	if got := chain[len(chain)-1].Source.Kind; got != fastconf.LayerPatch {
		t.Errorf("database.dsn winner kind = %v, want LayerPatch", got)
	}
}

type provSecretResolver struct{}

func (provSecretResolver) Recognize(v string) (fastconf.SecretRef, bool) {
	if rest, ok := strings.CutPrefix(v, "probe://"); ok {
		return fastconf.SecretRef{Scheme: "probe", Body: rest}, true
	}
	return fastconf.SecretRef{}, false
}

func (provSecretResolver) Resolve(_ context.Context, r fastconf.SecretRef) (string, error) {
	return "PLAINTEXT-" + r.Body, nil
}

type provSecretCfg struct {
	Items []struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	} `json:"items"`
}

func TestExplainDoesNotLeakResolvedSecretsThroughSliceLayers(t *testing.T) {
	fsys := fstest.MapFS{
		"conf.d/base/a.yaml": &fstest.MapFile{Data: []byte(
			"items:\n  - name: db\n    token: probe://dbpass\n")},
	}
	mgr, err := fastconf.New[provSecretCfg](context.Background(),
		fastconf.WithFS(fsys),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvenance(fastconf.ProvenanceFull),
		fastconf.WithSecretResolver(provSecretResolver{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// The decoded value still resolves, so the fix does not disable secrets.
	if got := mgr.Get().Items[0].Token; got != "PLAINTEXT-dbpass" {
		t.Fatalf("decoded token = %q, want the resolved plaintext", got)
	}

	origins := mgr.Snapshot().Explain("items")
	if len(origins) == 0 {
		t.Fatal("Explain(items) recorded no origin")
	}
	for _, o := range origins {
		rendered := renderProvValue(o.Value)
		if strings.Contains(rendered, "PLAINTEXT-") {
			t.Errorf("Explain(%q) leaked resolved plaintext: %s", o.Path, rendered)
		}
		if !strings.Contains(rendered, "probe://dbpass") {
			t.Errorf("Explain(%q) = %s, want the pre-resolution reference", o.Path, rendered)
		}
	}
}

func renderProvValue(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case string:
			b.WriteString(x)
			b.WriteByte(' ')
		}
	}
	walk(v)
	return b.String()
}
