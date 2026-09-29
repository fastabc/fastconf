package integration_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/policy"
)

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

func TestNoSources(t *testing.T) {
	mfs := fstest.MapFS{}
	_, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

// blockingProvider's Load blocks on <-ctx.Done() and returns ctx.Err()
// so we can prove that caller-side ctx threads into the pipeline.
type blockingProvider struct{ blocked chan struct{} }

func TestNew_ContextCancelsProvider(t *testing.T) {
	p := &blockingProvider{blocked: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		m, err := fastconf.New[map[string]any](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
		if m != nil {
			_ = m.Close()
		}
		done <- err
	}()
	await(t, p.blocked)
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("New = %v; want context.Canceled", err)
	}
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 100}
}

func (p *blockingProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	if p.blocked != nil {
		select {
		case p.blocked <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return contracts.Snapshot{}, ctx.Err()
}

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

// TestDefaultsAreStable guards against accidental changes to user-visible
// defaults that would silently break running deployments.
func TestDefaultsAreStable(t *testing.T) {
	if fastconf.DefaultDir != "conf.d" {
		t.Errorf("DefaultDir = %q, want %q", fastconf.DefaultDir, "conf.d")
	}
	if fastconf.DefaultProfileEnv != "APP_PROFILE" {
		t.Errorf("DefaultProfileEnv = %q, want %q", fastconf.DefaultProfileEnv, "APP_PROFILE")
	}
	if fastconf.DefaultCoalesceQuiet != 30*time.Millisecond {
		t.Errorf("DefaultCoalesceQuiet = %v, want 30ms", fastconf.DefaultCoalesceQuiet)
	}
	if fastconf.DefaultCoalesceMaxLag != 250*time.Millisecond {
		t.Errorf("DefaultCoalesceMaxLag = %v, want 250ms", fastconf.DefaultCoalesceMaxLag)
	}
	if fastconf.DefaultCoalesceSwapHint != 5*time.Millisecond {
		t.Errorf("DefaultCoalesceSwapHint = %v, want 5ms", fastconf.DefaultCoalesceSwapHint)
	}
}

func TestWithTenant_TagsCauseAndPolicy(t *testing.T) {
	var seen []string
	pol := policy.Func[appCfg]{N: "tenant", Fn: func(_ context.Context, in policy.Input[appCfg]) ([]policy.Violation, error) {
		seen = append(seen, in.Tenant)
		return nil, nil
	}}
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(newFS(nil)), fastconf.WithTenant("acme"), fastconf.WithPolicy(pol))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	if got := mgr.Snapshot().Cause().Tenant; got != "acme" {
		t.Fatalf("initial tenant = %q", got)
	}
	plan, err := mgr.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Proposed.Cause().Tenant; got != "acme" {
		t.Fatalf("preview tenant = %q", got)
	}
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{"database": map[string]any{"pool": 12}})); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Snapshot().Cause().Tenant; got != "acme" {
		t.Fatalf("reload tenant = %q", got)
	}
	if !slices.Equal(seen, []string{"acme", "acme", "acme"}) {
		t.Fatalf("policy tenants = %v", seen)
	}
}
