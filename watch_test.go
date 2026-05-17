package fastconf_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

func TestWatcher_HotReloadOnFileChange(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), `
server:
  addr: ":8080"
`)
	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), `
database:
  dsn: postgres://v1
  pool: 1
`)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithWatch(true),
		fastconf.WithCoalesceQuiet(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if mgr.Get().Database.DSN != "postgres://v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}

	gen1 := mgr.Snapshot().Generation

	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), `
database:
  dsn: postgres://v2-hot
  pool: 99
`)

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://v2-hot" }, "hot reload")
	if mgr.Snapshot().Generation == gen1 {
		t.Errorf("generation did not advance")
	}
}

func TestWatcher_FailedReloadKeepsState(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), `
database:
  dsn: ok
  pool: 1
`)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithWatch(true),
		fastconf.WithCoalesceQuiet(20*time.Millisecond),
		fastconf.WithStrict(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	gen1 := mgr.Snapshot().Generation

	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), "::: invalid yaml: [")
	time.Sleep(300 * time.Millisecond)

	if mgr.Snapshot().Generation != gen1 {
		t.Errorf("generation must not advance on failed reload")
	}
	if mgr.Get().Database.DSN != "ok" {
		t.Errorf("old state lost: %q", mgr.Get().Database.DSN)
	}
}

func TestSubscribe_FiresOnEveryReload(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	var dbCalls, srvCalls atomic.Int64
	fastconf.Subscribe(mgr, func(c *appCfg) *dbCfg { return &c.Database }, func(_, _ *dbCfg) { dbCalls.Add(1) })
	fastconf.Subscribe(mgr, func(c *appCfg) *string { return &c.Server.Addr }, func(_, _ *string) { srvCalls.Add(1) })

	// Subscribe fires on every commit; caller-side filtering is the user's job.
	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9000\"\nfeatures: [a, b]\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := srvCalls.Load(); got != 1 {
		t.Errorf("server subscriber: want 1, got %d", got)
	}
	if got := dbCalls.Load(); got != 1 {
		t.Errorf("database subscriber should also fire on every reload: want 1, got %d", got)
	}

	mfs["conf.d/base/20-database.yaml"] = &fstest.MapFile{Data: []byte("database:\n  dsn: \"postgres://overlay\"\n  pool: 10\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dbCalls.Load(); got != 2 {
		t.Errorf("database subscriber after db change: want 2, got %d", got)
	}
	if got := srvCalls.Load(); got != 2 {
		t.Errorf("server subscriber: want 2 (one per reload), got %d", got)
	}
}

func TestSubscribe_PanicIsolated(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	var goodCalls atomic.Int64
	fastconf.Subscribe(mgr, func(c *appCfg) *string { return &c.Server.Addr }, func(_, _ *string) { panic("boom") })
	fastconf.Subscribe(mgr, func(c *appCfg) *string { return &c.Server.Addr }, func(_, _ *string) { goodCalls.Add(1) })

	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9090\"\nfeatures: [a, b]\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatalf("reload should not bubble subscriber panic: %v", err)
	}
	if got := goodCalls.Load(); got != 1 {
		t.Errorf("good subscriber should still fire: got %d", got)
	}
}

func TestSubscribe_CancelStops(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	var calls atomic.Int64
	cancel := fastconf.Subscribe(mgr, func(c *appCfg) *string { return &c.Server.Addr }, func(_, _ *string) { calls.Add(1) })

	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9001\"\nfeatures: [a, b]\n")}
	_ = mgr.Reload(context.Background())
	cancel()
	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9002\"\nfeatures: [a, b]\n")}
	_ = mgr.Reload(context.Background())

	if got := calls.Load(); got != 1 {
		t.Errorf("after cancel: want 1, got %d", got)
	}
}

// TestWatcher_HotReloadOnOverlayProfileChange verifies that modifying a file
// inside a profile-specific overlay directory (overlays/<profile>/) triggers a
// hot-reload. BUG-1101: the old watcher only watched the static overlays/ root,
// missing the profile sub-directory entirely.
func TestWatcher_HotReloadOnOverlayProfileChange(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\n")
	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v1\n  pool: 5\n")

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProfile("production"),
		fastconf.WithWatch(true),
		fastconf.WithCoalesceQuiet(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if mgr.Get().Database.DSN != "postgres://prod-v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}
	gen1 := mgr.Snapshot().Generation

	// Modify the profile-specific overlay file.
	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v2\n  pool: 10\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://prod-v2" },
		"hot reload on profile overlay change")
	if mgr.Snapshot().Generation == gen1 {
		t.Errorf("generation did not advance after profile overlay change")
	}
}

// TestWatcher_HotReloadOnHierarchicalAxisChange verifies hot-reload fires when
// a multi-axis overlay file changes (BUG-1101: axis dirs must be watched too).
func TestWatcher_HotReloadOnHierarchicalAxisChange(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\n")
	// Axis overlay directory: conf.d/regions/eu/10-region.yaml
	writeFile(t, filepath.Join(conf, "regions", "eu", "10-region.yaml"),
		"database:\n  dsn: postgres://eu-v1\n  pool: 2\n")

	t.Setenv("FASTCONF_TEST_REGION", "eu")

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithMultiAxisOverlays(
			fastconf.OverlayAxis{Dir: "regions", EnvVar: "FASTCONF_TEST_REGION", Priority: 3000},
		),
		fastconf.WithWatch(true),
		fastconf.WithCoalesceQuiet(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if mgr.Get().Database.DSN != "postgres://eu-v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}
	gen1 := mgr.Snapshot().Generation

	// Modify the axis-specific overlay file.
	writeFile(t, filepath.Join(conf, "regions", "eu", "10-region.yaml"),
		"database:\n  dsn: postgres://eu-v2\n  pool: 20\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://eu-v2" },
		"hot reload on hierarchical axis overlay change")
	if mgr.Snapshot().Generation == gen1 {
		t.Errorf("generation did not advance after axis overlay change")
	}
}

func TestSubscribe_FiresWithCorrectTypes(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	var lastDSN atomic.Value
	cancel := fastconf.Subscribe(mgr,
		func(c *appCfg) *dbCfg { return &c.Database },
		func(_, neu *dbCfg) {
			if neu != nil {
				lastDSN.Store(neu.DSN)
			}
		},
	)
	defer cancel()

	mfs["conf.d/base/20-database.yaml"] = &fstest.MapFile{Data: []byte("database:\n  dsn: \"postgres://typed\"\n  pool: 20\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastDSN.Load(); got != "postgres://typed" {
		t.Errorf("Subscribe DSN: got %v", got)
	}
}
