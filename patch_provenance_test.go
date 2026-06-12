package fastconf_test

// A patch layer must attribute provenance only to the paths it names, not
// to the entire merged tree. Recording the whole tree (the pre-fix
// behavior) made Explain/LookupStrict report a one-key patch as the
// "winner" of every untouched key.

import (
	"context"
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
		fastconf.WithProfile(fastconf.ProfileOptions{Single: "prod"}),
		fastconf.WithProvenance(fastconf.ProvenanceFull),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

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
