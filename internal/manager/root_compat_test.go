package manager_test

// Tests sunk from root manager_test.go per SPEC-G1: they exercise
// internal/manager hash, swap, strict-merge, and bytes-source behavior.
// They live here so future white-box tests can share fixtures, while
// the root manager_test.go keeps only the facade-compatibility cases
// (TestNew_BaseOnly, TestNew_OverlayOverrides, TestNoSources, examples).

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/source"
)

type dbCfg struct {
	DSN  string `yaml:"dsn" json:"dsn"`
	Pool int    `yaml:"pool" json:"pool"`
}

type appCfg struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Database dbCfg    `yaml:"database" json:"database"`
	Features []string `yaml:"features" json:"features"`
}

func newFS(extra map[string]string) fstest.MapFS {
	fs := fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte(`
server:
  addr: ":8080"
features: [a, b]
`)},
		"conf.d/base/20-database.yaml": &fstest.MapFile{Data: []byte(`
database:
  dsn: "postgres://base"
  pool: 10
`)},
	}
	for k, v := range extra {
		fs[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return fs
}

func TestReload_IdenticalHashNoSwap(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	gen1 := mgr.Snapshot().Generation()
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen2 := mgr.Snapshot().Generation()
	if gen1 != gen2 {
		t.Errorf("generation should not change on identical reload: %d → %d", gen1, gen2)
	}
}

func TestReload_DifferentHashSwaps(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	gen1 := mgr.Snapshot().Generation()
	mfs["conf.d/base/20-database.yaml"] = &fstest.MapFile{Data: []byte("database:\n  dsn: changed\n  pool: 99\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation should advance after content change")
	}
	if mgr.Get().Database.DSN != "changed" {
		t.Errorf("did not pick up new content")
	}
}

func TestStrictMode_TypeMismatch(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00-a.yaml": &fstest.MapFile{Data: []byte("server:\n  addr: \":80\"\n")},
		"conf.d/base/01-b.yaml": &fstest.MapFile{Data: []byte("server: \"oops-string\"\n")},
	}
	_, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"), fastconf.WithStrict(true))
	if err == nil {
		t.Fatal("expected strict type mismatch error")
	}
}

func TestBytesSource_HighestPriority(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithSource(source.NewBytes("override", "yaml", []byte("database:\n  dsn: from-bytes\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.Get().Database.DSN != "from-bytes" {
		t.Errorf("bytes source did not win: %q", mgr.Get().Database.DSN)
	}
	// pool still comes from base
	if mgr.Get().Database.Pool != 10 {
		t.Errorf("pool lost: %d", mgr.Get().Database.Pool)
	}
}
