package fastconf_test

// H2: _meta.yaml changes merge semantics (strict, appendSlices,
// mergeKeys). A read error other than "not exist" must fail the reload,
// not silently degrade to "no meta".

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

// denyFS wraps a MapFS and returns ErrPermission for one path. The inner
// FS is a field (not embedded) so its ReadFile/ReadDir methods are not
// promoted and every access funnels through this Open.
type denyFS struct {
	inner fstest.MapFS
	deny  string
}

func (f denyFS) Open(name string) (fs.File, error) {
	if name == f.deny {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.inner.Open(name)
}

func TestMetaReadPermissionErrorFailsReload(t *testing.T) {
	inner := fstest.MapFS{
		"conf.d/_meta.yaml":       &fstest.MapFile{Data: []byte("apiVersion: v1\nspec:\n  strict: true\n")},
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: \":8080\"\n")},
		"conf.d/base/20-db.yaml":  &fstest.MapFile{Data: []byte("database:\n  dsn: x\n  pool: 1\n")},
	}
	fsys := denyFS{inner: inner, deny: "conf.d/_meta.yaml"}

	_, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(fsys),
		fastconf.WithDir("conf.d"),
	)
	if err == nil {
		t.Fatal("expected reload to fail when _meta.yaml is unreadable")
	}
	if !errors.Is(err, fastconf.ErrDecode) {
		t.Errorf("error %q does not chain to ErrDecode", err)
	}
}

func TestMetaAbsentStillLoads(t *testing.T) {
	// No _meta.yaml at all: the optional-file path must keep working.
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New without _meta.yaml: %v", err)
	}
	defer mgr.Close()
}
