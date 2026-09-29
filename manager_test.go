package fastconf_test

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/policy"
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
	defer func() { _ = mgr.Close() }()

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
	if snap.Generation() != 1 {
		t.Errorf("gen = %d", snap.Generation())
	}
	if len(snap.Sources()) != 2 {
		t.Errorf("sources = %d", len(snap.Sources()))
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
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	got := mgr.Get()
	if got.Database.DSN != "postgres://prod" || got.Database.Pool != 50 {
		t.Errorf("overlay not applied: %+v", got.Database)
	}
	if got.Server.Addr != ":8080" {
		t.Errorf("base lost: %q", got.Server.Addr)
	}
}

func TestCloseIdempotent(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestCloseConcurrentWithFailingReload exercises the caller-side error
// publish path (a Reload whose option fails publishes on the caller's
// goroutine, outside the bgWG-tracked reload loop) racing Close. Run
// with -race; a send on the closed Errors channel would panic here.
func TestCloseConcurrentWithFailingReload(t *testing.T) {
	bad := map[string]any{"ch": make(chan int)} // json.Marshal fails
	for i := 0; i < 50; i++ {
		mfs := newFS(nil)
		mgr, err := fastconf.New[appCfg](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = mgr.Reload(context.Background(), fastconf.WithOverride(bad))
			}()
		}
		if err := mgr.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		wg.Wait()
	}
}

func TestNoSources(t *testing.T) {
	mfs := fstest.MapFS{}
	_, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

// sinkInt prevents the compiler from optimising away the Get() call and
// gives the BenchmarkGet allocs report a stable consumer of the value.
var sinkInt int

func TestWithProvider_NilIsNoop(t *testing.T) {
	// Should not panic and should not append a provider entry.
	opt := fastconf.WithProvider(nil)
	if opt == nil {
		t.Fatal("WithProvider returned nil")
	}
}

// H2: _meta.yaml changes merge semantics (strict, appendSlices,
// mergeKeys). A read error other than "not exist" must fail the reload,
// not silently degrade to "no meta".

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
	defer func() { _ = mgr.Close() }()
}

type validatorCfg struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
}

func validatorFS(addr string) fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{
			Data: []byte("server:\n  addr: \"" + addr + "\"\n"),
		},
	}
}

func TestWithValidate_BlocksBadConfig(t *testing.T) {
	_, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS("")), fastconf.WithDir("conf.d"),
		fastconf.WithValidate(func(c *validatorCfg) error {
			if c.Server.Addr == "" {
				return errors.New("server.addr required")
			}
			return nil
		}),
	)
	if err == nil {
		t.Fatalf("expected validator error, got nil")
	}
	if !errors.Is(err, fastconf.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestWithValidate_AllowsGoodConfig(t *testing.T) {
	cfg, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS(":8080")), fastconf.WithDir("conf.d"),
		fastconf.WithValidate(func(c *validatorCfg) error {
			if c.Server.Addr == "" {
				return errors.New("required")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	if got := cfg.Get().Server.Addr; got != ":8080" {
		t.Fatalf("got %q", got)
	}
}

func TestWithValidate_NilSafe(t *testing.T) {
	_, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS(":1")), fastconf.WithDir("conf.d"),
		fastconf.WithValidate[validatorCfg](nil),
	)
	if err != nil {
		t.Fatalf("nil validator should be a no-op, got %v", err)
	}
}

// WithTransform must reject nil entries at construction. runTransform
// invokes Name()/Transform() on the reload goroutine with no recover, so a
// nil transformer would panic there and breach the fail-safe contract.

func TestWithTransformNilIsDeferredError(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	type cfg struct {
		Name string `json:"name"`
	}
	_, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransform(nil),
	)
	if err == nil {
		t.Fatal("expected nil transformer to surface as deferred error")
	}
	if !errors.Is(err, fastconf.ErrFastConf) {
		t.Errorf("error %q does not chain to ErrFastConf", err)
	}
	if !strings.Contains(err.Error(), "WithTransform") {
		t.Errorf("error %q does not mention WithTransform", err)
	}
}

// TestReloadOptionErrorPublishedOnce verifies: an invalid Reload option
// returns its error synchronously and publishes it once on Errors(); after
// Close the same call reports ErrClosed.
func TestReloadOptionErrorPublishedOnce(t *testing.T) {
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	bad := fastconf.WithOverride(map[string]any{"ch": make(chan int)})
	if err := mgr.Reload(context.Background(), bad); !errors.Is(err, fastconf.ErrDecode) {
		t.Fatalf("Reload = %v; want ErrDecode", err)
	}
	select {
	case re := <-mgr.Errors():
		if !errors.Is(re.Err, fastconf.ErrDecode) || re.Reason != "override" {
			t.Fatalf("published %+v; want ErrDecode with reason override", re)
		}
	case <-time.After(time.Second):
		t.Fatal("option error not published on Errors()")
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(context.Background(), bad); !errors.Is(err, fastconf.ErrClosed) {
		t.Fatalf("Reload after Close = %v; want ErrClosed", err)
	}
}

// TestWithTenantTagsCauseAndPolicy verifies: WithTenant replaces the
// TenantManager container; the tag reaches ReloadCause and policy input.
func TestWithTenantTagsCauseAndPolicy(t *testing.T) {
	var seen string
	pol := policy.Func[appCfg]{N: "tenant", Fn: func(_ context.Context, in policy.Input[appCfg]) ([]policy.Violation, error) {
		seen = in.Tenant
		return nil, nil
	}}
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(newFS(nil)), fastconf.WithDir("conf.d"),
		fastconf.WithTenant("acme"), fastconf.WithPolicy(pol),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Snapshot().Cause().Tenant; got != "acme" {
		t.Fatalf("Cause().Tenant = %q; want acme", got)
	}
	if seen != "acme" {
		t.Fatalf("policy saw tenant %q; want acme", seen)
	}
}

// TestLayoutGuard enforces the canonical root-package file layout. It should
// not be read as a blanket ban on every future split in the root package.
func TestLayoutGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	type rule struct {
		canonical       string
		forbiddenPrefix string
	}
	rules := []rule{
		{"options.go", "opt_"},
		{"manager.go", "manager_"},
		{"errors.go", "failure_"},
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	for _, r := range rules {
		if !have[r.canonical] {
			t.Fatalf("%s missing", r.canonical)
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			if strings.HasPrefix(n, r.forbiddenPrefix) {
				t.Fatalf("%s should be folded into %s", n, r.canonical)
			}
		}
	}

	// Keep regression coverage in the topic test file instead of bug_* files.
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bug_") {
			t.Fatalf("%s should be folded into the topic test file", e.Name())
		}
		if strings.HasPrefix(e.Name(), "example_") && e.Name() != "example_api_test.go" {
			t.Fatalf("%s belongs under examples/; keep only root package godoc examples here", e.Name())
		}
	}
}

// doc.go is the godoc landing page and must list the canonical "where do I
// start" surface so newcomers do not have to scan the entire alphabetised
// symbol index. This test fails if any of those symbols disappear from the
// package-level comment block.

func TestDocLanding_ListsRecommendedExports(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "doc.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse doc.go: %v", err)
	}
	if f.Doc == nil {
		t.Fatal("doc.go: package-level godoc comment block is missing")
	}
	doc := f.Doc.Text()
	required := []string{
		"New",
		"WithProfile", "WithWatch",
		"WithProvider", "WithAxes",
		"Subscribe", "Plan",
	}
	for _, sym := range required {
		if !strings.Contains(doc, sym) {
			t.Errorf("doc.go landing block missing recommended export %q", sym)
		}
	}
}
