package fastconf_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	k8s "github.com/fastabc/fastconf/providers/k8s"
	"github.com/fastabc/fastconf/providers/source"
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
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if mgr.Get().Database.DSN != "postgres://v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}

	gen1 := mgr.Snapshot().Generation()

	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), `
database:
  dsn: postgres://v2-hot
  pool: 99
`)

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://v2-hot" }, "hot reload")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance")
	}
}

// TestWatcher_SurvivesInitContextCancel verifies that the ctx passed to New
// bounds initialisation only. Canceling it afterwards must not stop the
// background file watcher.
func TestWatcher_SurvivesInitContextCancel(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "database:\n  dsn: v1\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	mgr, err := fastconf.New[appCfg](ctx,
		fastconf.WithDir(conf),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	// Give background goroutines a chance to observe the cancellation.
	time.Sleep(50 * time.Millisecond)
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "database:\n  dsn: v2\n")
	waitFor(t, func() bool { return mgr.Get().Database.DSN == "v2" }, "hot reload after init ctx cancel")
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
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
		fastconf.WithStrictMerge(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	gen1 := mgr.Snapshot().Generation()

	writeFile(t, filepath.Join(conf, "base", "20-database.yaml"), "::: invalid yaml: [")
	time.Sleep(300 * time.Millisecond)

	if mgr.Snapshot().Generation() != gen1 {
		t.Errorf("generation must not advance on failed reload")
	}
	if mgr.Get().Database.DSN != "ok" {
		t.Errorf("old state lost: %q", mgr.Get().Database.DSN)
	}
}

func TestSubscribe_PanicIsolated(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

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
	select {
	case re := <-mgr.Errors():
		if re.Reason != "subscriber-panic" || re.Err == nil {
			t.Errorf("subscriber panic error = %+v, want reason subscriber-panic with err", re)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("expected subscriber panic to surface on Errors()")
	}
}

func TestSubscribe_CancelStops(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

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
// hot-reload. Regression: an earlier watcher only watched the static
// overlays/ root and missed the profile sub-directory entirely.
func TestWatcher_HotReloadOnOverlayProfileChange(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\n")
	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v1\n  pool: 5\n")

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"production"}}),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if mgr.Get().Database.DSN != "postgres://prod-v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}
	gen1 := mgr.Snapshot().Generation()

	// Modify the profile-specific overlay file.
	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v2\n  pool: 10\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://prod-v2" },
		"hot reload on profile overlay change")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance after profile overlay change")
	}
}

func TestWatcher_AddsNewOverlayDirAfterReload(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\n")
	if err := os.MkdirAll(filepath.Join(conf, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"production"}}),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v1\n  pool: 5\n")
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Get().Database.DSN; got != "postgres://prod-v1" {
		t.Fatalf("after manual reload dsn: got %q", got)
	}
	gen1 := mgr.Snapshot().Generation()

	writeFile(t, filepath.Join(conf, "overlays", "production", "10-prod.yaml"),
		"database:\n  dsn: postgres://prod-v2\n  pool: 10\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://prod-v2" },
		"hot reload after dynamic overlay dir registration")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance after dynamic overlay dir change")
	}
}

func TestWatcher_AddsNewAxisDirAfterReload(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\n")
	if err := os.MkdirAll(filepath.Join(conf, "regions"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FASTCONF_TEST_REGION", "eu")

	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithAxes(
			fastconf.Axis{Dir: "regions", Env: "FASTCONF_TEST_REGION", Priority: 0},
		),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	writeFile(t, filepath.Join(conf, "regions", "eu", "10-region.yaml"),
		"database:\n  dsn: postgres://eu-v1\n  pool: 2\n")
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Get().Database.DSN; got != "postgres://eu-v1" {
		t.Fatalf("after manual reload dsn: got %q", got)
	}
	gen1 := mgr.Snapshot().Generation()

	writeFile(t, filepath.Join(conf, "regions", "eu", "10-region.yaml"),
		"database:\n  dsn: postgres://eu-v2\n  pool: 20\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://eu-v2" },
		"hot reload after dynamic axis dir registration")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance after dynamic axis dir change")
	}
}

// TestWatcher_HotReloadOnHierarchicalAxisChange verifies hot-reload fires when
// a multi-axis overlay file changes — axis dirs must be watched too.
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
		fastconf.WithAxes(
			fastconf.Axis{Dir: "regions", Env: "FASTCONF_TEST_REGION", Priority: 0},
		),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if mgr.Get().Database.DSN != "postgres://eu-v1" {
		t.Fatalf("initial dsn: %q", mgr.Get().Database.DSN)
	}
	gen1 := mgr.Snapshot().Generation()

	// Modify the axis-specific overlay file.
	writeFile(t, filepath.Join(conf, "regions", "eu", "10-region.yaml"),
		"database:\n  dsn: postgres://eu-v2\n  pool: 20\n")

	waitFor(t, func() bool { return mgr.Get().Database.DSN == "postgres://eu-v2" },
		"hot reload on hierarchical axis overlay change")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance after axis overlay change")
	}
}

type downwardWatchCfg struct {
	Labels map[string]string `json:"labels" yaml:"labels"`
}

// TestWatch_DownwardAPIAtomicSwapTriggersReload verifies that a provider which
// exposes projected-volume leaf paths participates in the shared K8s-aware
// filesystem watcher. The symlink layout mirrors a mounted Downward API
// volume: labels -> ..data/labels, with ..data atomically retargeted.
func TestWatch_DownwardAPIAtomicSwapTriggersReload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Kubernetes projected-volume symlink replacement requires Unix rename semantics")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-seed.yaml"), "seed: true\n")

	podinfo := filepath.Join(dir, "podinfo")
	if err := os.MkdirAll(filepath.Join(podinfo, "data-v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(podinfo, "data-v1", "labels"), "app=\"v1\"\n")
	if err := os.Symlink("data-v1", filepath.Join(podinfo, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "labels"), filepath.Join(podinfo, "labels")); err != nil {
		t.Fatal(err)
	}

	mgr, err := fastconf.New[downwardWatchCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProvider(k8s.New(k8s.Options{
			LabelsPath: filepath.Join(podinfo, "labels"),
		})),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Labels["app"]; got != "v1" {
		t.Fatalf("initial label app = %q want v1", got)
	}
	gen1 := mgr.Snapshot().Generation()

	if err := os.MkdirAll(filepath.Join(podinfo, "data-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(podinfo, "data-v2", "labels"), "app=\"v2\"\n")
	tmpLink := filepath.Join(podinfo, "..data_tmp_swap")
	if err := os.Symlink("data-v2", tmpLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpLink, filepath.Join(podinfo, "..data")); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return mgr.Get().Labels["app"] == "v2" }, "downward API reload after ..data swap")
	if mgr.Snapshot().Generation() == gen1 {
		t.Errorf("generation did not advance after downward API swap")
	}
}

func TestWatch_BoundFileAtomicReplaceTriggersReload(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00-seed.yaml"), "{}\n")
	path := filepath.Join(t.TempDir(), "external config.yaml")
	writeFile(t, path, "value: initial\n")
	type config struct {
		Value string `json:"value"`
	}
	mgr, err := fastconf.New[config](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Value; got != "initial" {
		t.Fatalf("initial value = %q", got)
	}

	// A second replacement proves the watch survives replacing the original file.
	for _, want := range []string{"second", "third"} {
		before := mgr.Snapshot().Generation()
		tmp := path + ".tmp"
		writeFile(t, tmp, "value: "+want+"\n")
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return mgr.Get().Value == want }, "bound file replacement: "+want)
		if mgr.Snapshot().Generation() <= before {
			t.Fatal("replacement did not publish a new generation")
		}
	}
}

// TestWithWatch_EnablesFileWatcher verifies: WithWatch(Watch{...}) turns the
// file watcher on and carries the coalescer windows directly.
func TestWithWatch_EnablesFileWatcher(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "database:\n  dsn: v1\n")
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond, Profile: fastconf.ProfileLocalDev}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "database:\n  dsn: v2\n")
	waitFor(t, func() bool { return mgr.Get().Database.DSN == "v2" }, "hot reload via Watch{}")
}

type vCfg struct {
	V int `json:"v"`
}

// A new overlay whose value equals the current config does not publish, but
// its directory must still be watched.
func TestWatcher_WatchesNewSourceWithUnchangedValue(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "v: 1\n")
	mgr, err := fastconf.New[vCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	gen := mgr.Snapshot().Generation()
	overlay := filepath.Join(conf, "overlays", "prod", "a.yaml")
	writeFile(t, overlay, "v: 1\n")
	// Let the reload triggered by creating the directory settle so it cannot
	// pick up the v2 edit below by coincidence.
	time.Sleep(200 * time.Millisecond)
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mgr.Snapshot().Generation() != gen {
		t.Fatalf("identical value should not publish")
	}
	writeFile(t, overlay, "v: 2\n")
	waitFor(t, func() bool { return mgr.Get().V == 2 }, "hot reload of overlay added with unchanged value")
}

// Removing and recreating a watched directory must re-register it.
func TestWatcher_RecreatedDirIsWatchedAgain(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "conf.d")
	base := filepath.Join(conf, "base")
	writeFile(t, filepath.Join(base, "00.yaml"), "v: 1\n")
	mgr, err := fastconf.New[vCfg](context.Background(),
		fastconf.WithDir(conf),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	writeFile(t, filepath.Join(base, "00.yaml"), "v: 2\n")
	waitFor(t, func() bool { return mgr.Get().V == 2 }, "reload after base dir recreation")
	writeFile(t, filepath.Join(base, "00.yaml"), "v: 3\n")
	waitFor(t, func() bool { return mgr.Get().V == 3 }, "edit inside recreated base dir")
}

// Creating a profile under a custom overlay parent must reload without a manual Reload.
func TestWatcher_CustomOverlayParent(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "conf.d")
	writeFile(t, filepath.Join(conf, "base", "00.yaml"), "v: 1\n")
	writeFile(t, filepath.Join(conf, "_meta.yaml"), "spec:\n  overlayDir: custom\n")
	if err := os.MkdirAll(filepath.Join(conf, "custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := fastconf.New[vCfg](context.Background(), fastconf.WithDir(conf),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithWatch(fastconf.Watch{Quiet: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	for i, parent := range []string{"custom", "other"} {
		if i > 0 {
			if err := os.MkdirAll(filepath.Join(conf, parent), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(conf, "_meta.yaml"), "spec:\n  overlayDir: "+parent+"\n")
			waitFor(t, func() bool { return mgr.Get().V == 1 }, "updated overlay metadata")
		}
		overlay := filepath.Join(conf, parent, "prod", "00.yaml")
		writeFile(t, overlay, "v: 2\n")
		waitFor(t, func() bool { return mgr.Get().V == 2 }, "new custom overlay")
		writeFile(t, overlay, "v: 3\n")
		waitFor(t, func() bool { return mgr.Get().V == 3 }, "custom overlay edit")
	}
}
