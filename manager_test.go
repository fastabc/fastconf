package fastconf_test

import (
	"context"
	"fmt"
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

func TestNew_BaseOnly(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	got := mgr.Get()
	if got.Server.Addr != ":8080" {
		t.Errorf("addr = %q", got.Server.Addr)
	}
	if got.Database.DSN != "postgres://base" || got.Database.Pool != 10 {
		t.Errorf("db = %+v", got.Database)
	}
	if len(got.Features) != 2 {
		t.Errorf("features = %v", got.Features)
	}
	snap := mgr.Snapshot()
	if snap.Generation != 1 {
		t.Errorf("gen = %d", snap.Generation)
	}
	if len(snap.Sources) != 2 {
		t.Errorf("sources = %d", len(snap.Sources))
	}
}

func TestNew_OverlayOverrides(t *testing.T) {
	mfs := newFS(map[string]string{
		"conf.d/overlays/prod/20-database.yaml": `
database:
  dsn: "postgres://prod"
  pool: 50
`,
	})
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile("prod"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	got := mgr.Get()
	if got.Database.DSN != "postgres://prod" || got.Database.Pool != 50 {
		t.Errorf("overlay not applied: %+v", got.Database)
	}
	if got.Server.Addr != ":8080" {
		t.Errorf("base lost: %q", got.Server.Addr)
	}
}

func TestReload_IdenticalHashNoSwap(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	gen1 := mgr.Snapshot().Generation
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen2 := mgr.Snapshot().Generation
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
	gen1 := mgr.Snapshot().Generation
	mfs["conf.d/base/20-database.yaml"] = &fstest.MapFile{Data: []byte("database:\n  dsn: changed\n  pool: 99\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mgr.Snapshot().Generation == gen1 {
		t.Errorf("generation should advance after content change")
	}
	if mgr.Get().Database.DSN != "changed" {
		t.Errorf("did not pick up new content")
	}
}

func TestNoSources(t *testing.T) {
	mfs := fstest.MapFS{}
	_, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err == nil {
		t.Fatal("expected error for empty config")
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
	// pool 仍来自 base
	if mgr.Get().Database.Pool != 10 {
		t.Errorf("pool lost: %d", mgr.Get().Database.Pool)
	}
}

// sinkInt prevents the compiler from optimising away the Get() call and
// gives the BenchmarkGet allocs report a stable consumer of the value.
var sinkInt int

func BenchmarkGet(b *testing.B) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = mgr.Get().Database.Pool
	}
}

func BenchmarkGetParallel(b *testing.B) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var local int
		for pb.Next() {
			local = mgr.Get().Database.Pool
		}
		sinkInt = local
	})
}

// BenchmarkReloadLarge exercises the full reload pipeline against a
// synthetic 256-key configuration. It is intended as a regression
// guard for assemble + merge + decode allocations.
func BenchmarkReloadLarge(b *testing.B) {
	const n = 256
	mfs := fstest.MapFS{}
	for i := 0; i < n; i++ {
		mfs[fmt.Sprintf("conf.d/base/%03d.yaml", i)] = &fstest.MapFile{
			Data: []byte(fmt.Sprintf("k%03d: %d\n", i, i)),
		}
	}
	mgr, err := fastconf.New[map[string]any](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
